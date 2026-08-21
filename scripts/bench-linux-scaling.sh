#!/usr/bin/env bash
# AMD/Linux scaling, confirmation, and profiling runner for Dirtybird Go Miner.
# It only runs binaries supplied by the caller; it never builds or downloads.

set -Eeuo pipefail

SCRIPT_NAME=${0##*/}
SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
MODE=
OUT_ROOT=bench-results/linux-scaling
LABEL=run
SECS=120
COOLDOWN_SECS=30
PRE_BLOCK_COOLDOWN_SECS=0
MINER_PIN=false
RUN_DIR=
ARCHIVE=

usage() {
    cat <<'EOF'
Usage:
  bench-linux-scaling.sh screen --baseline PATH --current-v3 PATH [--current-v4 PATH]
      [--v4-test-binary PATH]
      [--secs 120] [--cooldown 30] [--miner-pin] [--out-root DIR] [--label NAME]

  bench-linux-scaling.sh confirm --base PATH --candidate PATH
      [--base-kind baseline|v3|v4] [--candidate-kind baseline|v3|v4]
      [--base-pair x1|x2] [--candidate-pair x1|x2]
      [--v4-test-binary PATH]
      [--threads N|physical|logical] [--secs 240] [--cooldown 20]
      [--miner-pin | --base-pin | --candidate-pin]
      [--out-root DIR] [--label NAME]

  bench-linux-scaling.sh profile --binary PATH [--kind v3|v4] [--pair x1|x2]
      [--v4-test-binary PATH] [--uprof PATH]
      [--secs 120] [--miner-pin] [--out-root DIR] [--label NAME]

  bench-linux-scaling.sh --self-test

screen runs one discarded warm-up, then each arm forward and backward at
topology-derived thread counts. confirm emits legs.csv in the format consumed
by scripts/analyze-thue-morse.py. profile runs pprof and, separately, perf stat.
EOF
}

die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
warn() { printf 'WARN: %s\n' "$*" >&2; }

need_command() {
    command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

positive_int() {
    [[ $2 =~ ^[1-9][0-9]*$ ]] || die "$1 must be a positive integer (got '$2')"
}

nonnegative_int() {
    [[ $2 =~ ^[0-9]+$ ]] || die "$1 must be a non-negative integer (got '$2')"
}

validate_label() {
    [[ $1 =~ ^[A-Za-z0-9._-]+$ ]] || die "label must contain only letters, digits, '.', '_', or '-'"
}

require_binary() {
    [[ -f $2 ]] || die "$1 binary not found: $2"
    [[ -x $2 ]] || die "$1 binary is not executable: $2"
}

pair_bool() {
    case $1 in
        x1) printf 'false\n' ;;
        x2) printf 'true\n' ;;
        *) die "pair mode must be x1 or x2 (got '$1')" ;;
    esac
}

expected_avx512() {
    case $1 in
        baseline) printf 'ignore\n' ;;
        v3) printf 'false\n' ;;
        v4) printf 'true\n' ;;
        *) die "binary kind must be baseline, v3, or v4 (got '$1')" ;;
    esac
}

csv_quote() {
    local value=${1//\"/\"\"}
    printf '"%s"' "$value"
}

csv_row() {
    local first=true value
    for value in "$@"; do
        if $first; then first=false; else printf ','; fi
        csv_quote "$value"
    done
    printf '\n'
}

json_escape() {
    local value=$1
    value=${value//\\/\\\\}
    value=${value//\"/\\\"}
    value=${value//$'\n'/\\n}
    value=${value//$'\r'/\\r}
    value=${value//$'\t'/\\t}
    printf '%s' "$value"
}

utc_now() { date -u +'%Y-%m-%dT%H:%M:%SZ'; }

hash_file() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print $1}'
    else
        die "sha256sum or shasum is required"
    fi
}

go_build_setting() {
    local binary=$1 key=$2
    command -v go >/dev/null 2>&1 || return 1
    go version -m "$binary" 2>/dev/null | awk -v prefix="$key=" '
        $1 == "build" && index($2, prefix) == 1 {
            print substr($2, length(prefix) + 1)
            found = 1
            exit
        }
        END { if (!found) exit 1 }
    '
}

go_binary_toolchain() {
    command -v go >/dev/null 2>&1 || return 1
    go version -m "$1" 2>/dev/null | awk 'NR == 1 { print $NF; found = 1 } END { if (!found) exit 1 }'
}

record_provenance() {
    printf '%s\n' "$*" >>"$RUN_DIR/validation/build-provenance.txt"
}

# Expand Linux cpulist syntax such as 0-3,8,10-11, one CPU per line.
expand_cpulist() {
    local list=$1 part start end cpu
    IFS=',' read -r -a parts <<<"$list"
    for part in "${parts[@]}"; do
        [[ -n $part ]] || continue
        if [[ $part == *-* ]]; then
            start=${part%%-*}
            end=${part##*-}
            [[ $start =~ ^[0-9]+$ && $end =~ ^[0-9]+$ && $start -le $end ]] || return 1
            for ((cpu = start; cpu <= end; cpu++)); do printf '%d\n' "$cpu"; done
        else
            [[ $part =~ ^[0-9]+$ ]] || return 1
            printf '%d\n' "$part"
        fi
    done
}

join_by_comma() {
    local IFS=,
    printf '%s' "$*"
}

read_sysfs_value() {
    local path=$1 fallback=$2
    if [[ -r $path ]]; then tr -d '[:space:]' <"$path"; else printf '%s' "$fallback"; fi
}

# Return package:level:cache-id for the highest data/unified cache visible to CPU.
llc_key_for_cpu() {
    local cpu=$1 package=$2 die=$3 dir level type id best_level=-1 best_id=
    for dir in /sys/devices/system/cpu/cpu"$cpu"/cache/index*; do
        [[ -d $dir ]] || continue
        level=$(read_sysfs_value "$dir/level" '')
        type=$(read_sysfs_value "$dir/type" '')
        id=$(read_sysfs_value "$dir/id" '')
        [[ $level =~ ^[0-9]+$ && ( $type == Unified || $type == Data ) && -n $id ]] || continue
        if ((level > best_level)); then
            best_level=$level
            best_id=$id
        fi
    done
    if ((best_level >= 0)); then printf '%s:%s:%s:%s\n' "$package" "$die" "$best_level" "$best_id"; fi
}

declare -a ALLOWED_CPUS=()
declare -a PHYSICAL_CPUS=()
declare -a SMT_CPUS=()
declare -a ORDERED_CPUS=()
ALLOWED_LIST=
PHYSICAL_COUNT=0
LOGICAL_COUNT=0
LLC_BOUNDARY=0

discover_topology() {
    local line cpu package die core core_key llc_key first_size=-1 size
    local -A seen_core=() llc_sizes=() llc_seen=()
    local -a reps=() rep_llcs=() llc_order=() ordered_reps=()

    line=$(awk '/^Cpus_allowed_list:/ {print $2; exit}' /proc/self/status 2>/dev/null || true)
    if [[ -z $line && -r /sys/devices/system/cpu/online ]]; then
        line=$(tr -d '[:space:]' </sys/devices/system/cpu/online)
    fi
    [[ -n $line ]] || die "could not determine the allowed Linux CPU set"
    mapfile -t ALLOWED_CPUS < <(expand_cpulist "$line")
    ((${#ALLOWED_CPUS[@]} > 0)) || die "allowed CPU set is empty"
    ALLOWED_LIST=$(join_by_comma "${ALLOWED_CPUS[@]}")

    for cpu in "${ALLOWED_CPUS[@]}"; do
        package=$(read_sysfs_value "/sys/devices/system/cpu/cpu$cpu/topology/physical_package_id" 0)
        die=$(read_sysfs_value "/sys/devices/system/cpu/cpu$cpu/topology/die_id" 0)
        core=$(read_sysfs_value "/sys/devices/system/cpu/cpu$cpu/topology/core_id" "$cpu")
        core_key=$package:$die:$core
        if [[ -z ${seen_core[$core_key]+x} ]]; then
            seen_core[$core_key]=1
            reps+=("$cpu")
            llc_key=$(llc_key_for_cpu "$cpu" "$package" "$die")
            rep_llcs+=("$llc_key")
            if [[ -n $llc_key ]]; then
                llc_sizes[$llc_key]=$(( ${llc_sizes[$llc_key]:-0} + 1 ))
                if [[ -z ${llc_seen[$llc_key]+x} ]]; then
                    llc_seen[$llc_key]=1
                    llc_order+=("$llc_key")
                fi
            fi
        else
            SMT_CPUS+=("$cpu")
        fi
    done

    # Keep each LLC/CCD together so the detected one-LLC boundary selects it.
    if ((${#llc_order[@]} > 0)); then
        for llc_key in "${llc_order[@]}"; do
            for ((cpu = 0; cpu < ${#reps[@]}; cpu++)); do
                [[ ${rep_llcs[$cpu]} == "$llc_key" ]] && ordered_reps+=("${reps[$cpu]}")
            done
        done
        for ((cpu = 0; cpu < ${#reps[@]}; cpu++)); do
            [[ -z ${rep_llcs[$cpu]} ]] && ordered_reps+=("${reps[$cpu]}")
        done
        PHYSICAL_CPUS=("${ordered_reps[@]}")
    else
        PHYSICAL_CPUS=("${reps[@]}")
    fi

    PHYSICAL_COUNT=${#PHYSICAL_CPUS[@]}
    LOGICAL_COUNT=${#ALLOWED_CPUS[@]}
    ORDERED_CPUS=("${PHYSICAL_CPUS[@]}" "${SMT_CPUS[@]}")

    # A boundary is safe only when every allowed LLC has the same nonzero core count.
    if ((${#llc_order[@]} > 1)); then
        for llc_key in "${llc_order[@]}"; do
            size=${llc_sizes[$llc_key]}
            if ((first_size < 0)); then first_size=$size; fi
            if ((size != first_size)); then first_size=0; break; fi
        done
        if ((first_size > 0 && first_size < PHYSICAL_COUNT)); then LLC_BOUNDARY=$first_size; fi
    fi
}

capture_topology() {
    local index cpu package die core llc role physical_cpu
    local -A physical=()
    for physical_cpu in "${PHYSICAL_CPUS[@]}"; do physical[$physical_cpu]=1; done
    {
        csv_row order cpu role package die core llc
        for ((index=0; index<${#ORDERED_CPUS[@]}; index++)); do
            cpu=${ORDERED_CPUS[$index]}
            package=$(read_sysfs_value "/sys/devices/system/cpu/cpu$cpu/topology/physical_package_id" 0)
            die=$(read_sysfs_value "/sys/devices/system/cpu/cpu$cpu/topology/die_id" 0)
            core=$(read_sysfs_value "/sys/devices/system/cpu/cpu$cpu/topology/core_id" "$cpu")
            llc=$(llc_key_for_cpu "$cpu" "$package" "$die")
            if [[ -n ${physical[$cpu]+x} ]]; then role=physical; else role=smt; fi
            csv_row "$index" "$cpu" "$role" "$package" "$die" "$core" "$llc"
        done
    } >"$RUN_DIR/host/topology.csv"
}

cpus_for_threads() {
    local threads=$1
    ((threads >= 1 && threads <= ${#ORDERED_CPUS[@]})) || die "thread count $threads exceeds allowed CPUs"
    local selected=("${ORDERED_CPUS[@]:0:threads}")
    join_by_comma "${selected[@]}"
}

cpu_supports_v4() {
    local flags
    flags=" $(awk -F: '/^(flags|Features)[[:space:]]*:/ {print $2; exit}' /proc/cpuinfo 2>/dev/null) "
    local feature
    for feature in avx512f avx512bw avx512cd avx512dq avx512vl; do
        [[ $flags == *" $feature "* ]] || return 1
    done
}

capture_host() {
    mkdir -p "$RUN_DIR/host"
    capture_topology
    cp -- "$SCRIPT_DIR/$SCRIPT_NAME" "$RUN_DIR/host/$SCRIPT_NAME"
    if [[ -f $SCRIPT_DIR/analyze-thue-morse.py ]]; then
        cp -- "$SCRIPT_DIR/analyze-thue-morse.py" "$RUN_DIR/host/analyze-thue-morse.py"
    fi
    uname -a >"$RUN_DIR/host/uname.txt"
    printf '%s\n' "$ALLOWED_LIST" >"$RUN_DIR/host/cpus-allowed-list.txt"
    if command -v lscpu >/dev/null 2>&1; then
        lscpu >"$RUN_DIR/host/lscpu.txt" 2>&1 || true
        lscpu -e=CPU,NODE,SOCKET,CORE,ONLINE,MAXMHZ,MINMHZ >"$RUN_DIR/host/lscpu-extended.txt" 2>&1 || true
    else
        warn "lscpu not found; using /proc and sysfs topology only"
    fi
    awk -F: '/^(vendor_id|model name|flags|Features)[[:space:]]*:/ {print}' /proc/cpuinfo >"$RUN_DIR/host/cpuinfo-summary.txt" 2>/dev/null || true
    [[ ! -r /proc/loadavg ]] || cp /proc/loadavg "$RUN_DIR/host/loadavg.txt"
    [[ ! -r /proc/uptime ]] || cp /proc/uptime "$RUN_DIR/host/uptime.txt"
    : >"$RUN_DIR/host/governors.txt"
    local path
    for path in /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor; do
        [[ -r $path ]] && printf '%s=%s\n' "$path" "$(<"$path")" >>"$RUN_DIR/host/governors.txt"
    done
    return 0
}

capture_telemetry() {
    local event=$1 path type value
    for path in /sys/class/thermal/thermal_zone*/temp /sys/class/hwmon/hwmon*/temp*_input; do
        [[ -r $path ]] || continue
        type=${path%/*}
        if [[ -r $type/type ]]; then type=$(<"$type/type"); else type=${path%/*}; fi
        value=$(<"$path")
        csv_row "$(utc_now)" "$event" "$type" "$path" "$value" >>"$RUN_DIR/telemetry.csv"
    done
}

configure_pre_block_cooldown() {
    PRE_BLOCK_COOLDOWN_SECS=180
    if ((COOLDOWN_SECS > PRE_BLOCK_COOLDOWN_SECS)); then
        PRE_BLOCK_COOLDOWN_SECS=$COOLDOWN_SECS
    fi
}

run_pre_block_cooldown() {
    ((PRE_BLOCK_COOLDOWN_SECS > 0)) || return 0
    printf 'post-correctness idle cooldown: %ss\n' "$PRE_BLOCK_COOLDOWN_SECS"
    capture_telemetry correctness-cooldown-before
    sleep "$PRE_BLOCK_COOLDOWN_SECS"
    capture_telemetry correctness-cooldown-after
}

record_binary() {
    local label=$1 path=$2 kind=$3 pair=$4
    local digest
    digest=$(hash_file "$path")
    csv_row "$label" "$path" "$kind" "$pair" "$digest" >>"$RUN_DIR/binaries.csv"
    if command -v go >/dev/null 2>&1; then
        go version -m "$path" >"$RUN_DIR/host/${label}-go-version.txt" 2>&1 || true
    fi
}

run_miner_selftest() {
    local label=$1 binary=$2 status
    local log=$RUN_DIR/validation/$label-selftest.log
    mkdir -p "$RUN_DIR/validation"
    printf 'selftest: %s ... ' "$label"
    set +e
    "$binary" --selftest >"$log" 2>&1
    status=$?
    set -e
    [[ $status -eq 0 ]] || die "$label --selftest exited $status (see $log)"
    grep -q 'PASS' "$log" || die "$label --selftest did not report PASS (see $log)"
    printf 'PASS\n'
}

run_miner_version() {
    local label=$1 binary=$2 expected=${3:-} status reported
    local log=$RUN_DIR/validation/$label-version.log
    mkdir -p "$RUN_DIR/validation"
    set +e
    "$binary" --version >"$log" 2>&1
    status=$?
    set -e
    [[ $status -eq 0 ]] || die "$label --version exited $status (see $log)"
    if [[ -n $expected ]] && ! grep -Fq -- "$expected" "$log"; then
        reported=$(grep -Eo 'v[0-9]+\.[0-9]+\.[0-9]+' "$log" | head -1 || true)
        if [[ -n $reported ]]; then
            die "$label reports $reported; expected $expected (see $log)"
        fi
        warn "$label did not expose a semantic version; expected $expected (recorded in $log)"
    fi
}

gate_reports_zero_fallbacks() {
    grep -Eq '^V114 GATE: [0-9]+/[0-9]+ hashes matched, 0 fallbacks$' "$1"
}

run_v4_test_binary() {
    local binary=$1 focused_log=$RUN_DIR/validation/v4-focused-tests.log gate_log=$RUN_DIR/validation/v4-million-gate.log status test_name
    local focused='^(TestV114FastPaths|TestV114DifferentialVsSAIS|TestPairDifferentialVsSingle|TestHashPairMatchesHash|TestSHA256SingleNIMatchesStdlib)$'
    printf 'v4 focused correctness tests ... '
    set +e
    "$binary" -test.v -test.count=1 -test.timeout=10m -test.run "$focused" >"$focused_log" 2>&1
    status=$?
    set -e
    [[ $status -eq 0 ]] || die "v4 focused tests exited $status (see $focused_log)"
    for test_name in TestV114FastPaths TestV114DifferentialVsSAIS TestPairDifferentialVsSingle TestHashPairMatchesHash TestSHA256SingleNIMatchesStdlib; do
        grep -q -- "--- PASS: $test_name" "$focused_log" || die "$test_name did not pass (see $focused_log)"
    done
    grep -q -- 'AVX512MiningPath=true' "$focused_log" || die "test binary is not a GOAMD64=v4 build (see $focused_log)"
    printf 'PASS\n'

    printf 'v4 1,000,008-hash differential gate ... '
    set +e
    V114_GATE_HASHES=1000008 "$binary" -test.v -test.count=1 -test.timeout=40m -test.run '^TestV114MillionHashGate$' >"$gate_log" 2>&1
    status=$?
    set -e
    [[ $status -eq 0 ]] || die "v4 million-hash gate exited $status (see $gate_log)"
    grep -q -- '--- PASS: TestV114MillionHashGate' "$gate_log" || die "million-hash gate did not pass (see $gate_log)"
    gate_reports_zero_fallbacks "$gate_log" || die "million-hash gate did not report zero fallbacks (see $gate_log)"
    printf 'PASS\n'
}

validate_v4_build_metadata() {
    local miner=$1 tests=$2 miner_level tests_level miner_revision tests_revision miner_modified tests_modified
    miner_level=$(go_build_setting "$miner" GOAMD64 || true)
    tests_level=$(go_build_setting "$tests" GOAMD64 || true)
    if [[ -n $miner_level || -n $tests_level ]]; then
        [[ $miner_level == v4 ]] || die "current-v4 build metadata reports GOAMD64=${miner_level:-unknown}, expected v4"
        [[ $tests_level == v4 ]] || die "v4 test build metadata reports GOAMD64=${tests_level:-unknown}, expected v4"
        record_provenance "PASS v4 miner/test GOAMD64=v4"
    else
        record_provenance "UNPROVEN v4 GOAMD64 metadata unavailable; runtime test marker required"
    fi

    miner_revision=$(go_build_setting "$miner" vcs.revision || true)
    tests_revision=$(go_build_setting "$tests" vcs.revision || true)
    miner_modified=$(go_build_setting "$miner" vcs.modified || true)
    tests_modified=$(go_build_setting "$tests" vcs.modified || true)
    if [[ -n $miner_revision && -n $tests_revision ]]; then
        [[ $miner_revision == "$tests_revision" && $miner_modified == "$tests_modified" ]] || die "current-v4 and v4 tests have different VCS provenance"
        if [[ $miner_modified == true ]]; then
            warn "v4 miner/tests share a revision but contain uncommitted changes; strict same-source provenance is unproven"
            record_provenance "UNPROVEN v4 miner/test vcs.revision=$miner_revision vcs.modified=true"
        else
            record_provenance "PASS v4 miner/test vcs.revision=$miner_revision vcs.modified=${miner_modified:-unknown}"
        fi
    else
        warn "VCS revision metadata unavailable; the v4 runtime marker is enforced, but same-source provenance remains caller-supplied"
        record_provenance "UNPROVEN v4 miner/test VCS revision; build info and hashes recorded"
    fi
}

validate_current_builds() {
    local v3=$1 v4=${2:-} v3_level v4_level v3_toolchain v4_toolchain
    local v3_revision v4_revision v3_modified v4_modified v3_pgo v4_pgo
    if ! command -v go >/dev/null 2>&1; then
        warn "go not found; current binary GOAMD64/toolchain/source/PGO provenance is unproven"
        record_provenance "UNPROVEN go command unavailable; current build metadata not validated"
        return
    fi

    v3_level=$(go_build_setting "$v3" GOAMD64 || true)
    [[ $v3_level == v3 ]] || die "current-v3 build metadata reports GOAMD64=${v3_level:-unknown}, expected v3"
    record_provenance "PASS current-v3 GOAMD64=v3"
    [[ -n $v4 ]] || return

    v4_level=$(go_build_setting "$v4" GOAMD64 || true)
    [[ $v4_level == v4 ]] || die "current-v4 build metadata reports GOAMD64=${v4_level:-unknown}, expected v4"
    record_provenance "PASS current-v4 GOAMD64=v4"

    v3_toolchain=$(go_binary_toolchain "$v3" || true)
    v4_toolchain=$(go_binary_toolchain "$v4" || true)
    [[ -n $v3_toolchain && $v3_toolchain == "$v4_toolchain" ]] || die "current-v3/current-v4 Go toolchains differ or are unavailable"
    record_provenance "PASS current-v3/current-v4 toolchain=$v3_toolchain"

    v3_revision=$(go_build_setting "$v3" vcs.revision || true)
    v4_revision=$(go_build_setting "$v4" vcs.revision || true)
    v3_modified=$(go_build_setting "$v3" vcs.modified || true)
    v4_modified=$(go_build_setting "$v4" vcs.modified || true)
    if [[ -n $v3_revision && -n $v4_revision ]]; then
        [[ $v3_revision == "$v4_revision" && $v3_modified == "$v4_modified" ]] || die "current-v3/current-v4 VCS provenance differs"
        if [[ $v3_modified == true ]]; then
            warn "current builds share a revision but contain uncommitted changes; strict same-source provenance is unproven"
            record_provenance "UNPROVEN current builds vcs.revision=$v3_revision vcs.modified=true"
        else
            record_provenance "PASS current builds vcs.revision=$v3_revision vcs.modified=${v3_modified:-unknown}"
        fi
    else
        warn "current-v3/current-v4 VCS revision metadata unavailable; strict same-source provenance is unproven"
        record_provenance "UNPROVEN current-v3/current-v4 VCS revision metadata unavailable"
    fi

    v3_pgo=$(go_build_setting "$v3" -pgo || true)
    v4_pgo=$(go_build_setting "$v4" -pgo || true)
    if [[ -n $v3_pgo || -n $v4_pgo ]]; then
        [[ -n $v3_pgo && $v3_pgo == "$v4_pgo" ]] || die "current-v3/current-v4 PGO build settings differ"
        record_provenance "PASS current-v3/current-v4 -pgo=$v3_pgo"
    else
        warn "PGO build settings are absent from Go metadata; matching PGO inputs are unproven"
        record_provenance "UNPROVEN current-v3/current-v4 PGO settings unavailable"
    fi
}

write_manifest() {
    local mode=$1 secs=$2 cooldown=$3 threads=${4:-} base_pin=${5:-$MINER_PIN} candidate_pin=${6:-$MINER_PIN}
    printf '{\n' >"$RUN_DIR/manifest.json"
    printf '  "schema": 1,\n' >>"$RUN_DIR/manifest.json"
    printf '  "mode": "%s",\n' "$(json_escape "$mode")" >>"$RUN_DIR/manifest.json"
    printf '  "label": "%s",\n' "$(json_escape "$LABEL")" >>"$RUN_DIR/manifest.json"
    printf '  "started_utc": "%s",\n' "$(utc_now)" >>"$RUN_DIR/manifest.json"
    printf '  "hostname": "%s",\n' "$(json_escape "$(hostname 2>/dev/null || printf unknown)")" >>"$RUN_DIR/manifest.json"
    printf '  "seconds": %d,\n' "$secs" >>"$RUN_DIR/manifest.json"
    printf '  "cooldown_seconds": %d,\n' "$cooldown" >>"$RUN_DIR/manifest.json"
    printf '  "pre_block_cooldown_seconds": %d,\n' "$PRE_BLOCK_COOLDOWN_SECS" >>"$RUN_DIR/manifest.json"
    printf '  "miner_pin": %s,\n' "$MINER_PIN" >>"$RUN_DIR/manifest.json"
    printf '  "base_pin": %s,\n' "$base_pin" >>"$RUN_DIR/manifest.json"
    printf '  "candidate_pin": %s,\n' "$candidate_pin" >>"$RUN_DIR/manifest.json"
    printf '  "allowed_cpus": "%s",\n' "$ALLOWED_LIST" >>"$RUN_DIR/manifest.json"
    printf '  "physical_cores_allowed": %d,\n' "$PHYSICAL_COUNT" >>"$RUN_DIR/manifest.json"
    printf '  "logical_cpus_allowed": %d,\n' "$LOGICAL_COUNT" >>"$RUN_DIR/manifest.json"
    printf '  "one_llc_physical_boundary": %d,\n' "$LLC_BOUNDARY" >>"$RUN_DIR/manifest.json"
    printf '  "threads": "%s"\n' "$(json_escape "$threads")" >>"$RUN_DIR/manifest.json"
    printf '}\n' >>"$RUN_DIR/manifest.json"
}

start_results() {
    local mode=$1
    validate_label "$LABEL"
    local stamp
    stamp=$(date -u +'%Y%m%d-%H%M%S')
    mkdir -p "$OUT_ROOT/$mode"
    RUN_DIR=$OUT_ROOT/$mode/$stamp-$LABEL
    [[ ! -e $RUN_DIR ]] || RUN_DIR=$RUN_DIR-$$
    mkdir -p "$RUN_DIR/raw"
    mkdir -p "$RUN_DIR/validation"
    : >"$RUN_DIR/validation/build-provenance.txt"
    csv_row label path kind pair sha256 >"$RUN_DIR/binaries.csv"
    csv_row timestamp event sensor path raw_value >"$RUN_DIR/telemetry.csv"
    capture_host
}

finalize_results() {
    local digest
    printf '%s\n' "$(utc_now)" >"$RUN_DIR/completed-utc.txt"
    (
        cd "$RUN_DIR"
        : >checksums.sha256
        while IFS= read -r -d '' file; do
            digest=$(hash_file "$file")
            printf '%s  %s\n' "$digest" "${file#./}" >>checksums.sha256
        done < <(find . -type f ! -name checksums.sha256 -print0)
    )
    ARCHIVE=$RUN_DIR.tar.gz
    tar -czf "$ARCHIVE" -C "$(dirname "$RUN_DIR")" "$(basename "$RUN_DIR")"
    printf 'results: %s\narchive: %s\n' "$RUN_DIR" "$ARCHIVE"
}

validate_sustained_header() {
    local log=$1 threads=$2 pair=$3 avx=$4 miner_pin=${5:-$MINER_PIN}
    grep -Eq "sustained bench: ${threads} threads,.*pin=${miner_pin},.*sa=v114,.*pipeline=${pair}([[:space:]]|$)" "$log" ||
        die "sustained header mismatch: expected threads=$threads, pin=$miner_pin, sa=v114, pipeline=$pair (see $log)"
    if [[ $avx != ignore ]]; then
        grep -Eq "sustained bench: ${threads} threads,.*avx512=${avx},.*pipeline=${pair}([[:space:]]|$)" "$log" ||
            die "sustained header mismatch: expected avx512=$avx and pipeline=$pair (see $log)"
    fi
}

parse_whole_rate() {
    awk '
        /^[0-9]+ hashes in .* = [0-9.]+ KH\/s/ {
            hashes=$1
            for (i=1; i<=NF; i++) if ($i == "KH/s") khs=$(i-1)
        }
        END { if (hashes == "" || khs == "") exit 1; printf "%s %.6f\n", hashes, khs }
    ' "$1"
}

parse_steady_rate() {
    awk '
        /^120\+/ {
            duration=$2
            sub(/^t=/, "", duration)
            seconds=0
            if (duration ~ /m/) {
                split(duration, part, "m")
                seconds=60*part[1]
                duration=part[2]
            }
            sub(/s$/, "", duration)
            seconds += duration
            if (seconds > 120) { sum += $4; count++ }
        }
        END { if (!count) exit 1; printf "%.6f\n", sum/count }
    ' "$1"
}

run_sustained() {
    local binary=$1 threads=$2 cpus=$3 secs=$4 pair=$5 avx=$6 log=$7 profile=${8:-}
    local miner_pin=${9:-$MINER_PIN} pair_flag status start end
    pair_flag=$(pair_bool "$pair")
    local -a args=(--sustained --secs "$secs" -t "$threads" --sa v114 "--pair=$pair_flag")
    if $miner_pin; then args+=(--pin); else args+=(--pin=false); fi
    if [[ -n $profile ]]; then args+=(--cpuprofile "$profile"); fi

    start=$(utc_now)
    {
        printf '# started_utc=%s\n' "$start"
        printf '# cpus=%s GOMAXPROCS=%s\n' "$cpus" "$threads"
        printf '# command:'
        printf ' %q' taskset -c "$cpus" env "GOMAXPROCS=$threads" "$binary" "${args[@]}"
        printf '\n'
    } >"$log"
    set +e
    taskset -c "$cpus" env "GOMAXPROCS=$threads" "$binary" "${args[@]}" >>"$log" 2>&1
    status=$?
    set -e
    end=$(utc_now)
    [[ $status -eq 0 ]] || die "benchmark exited $status (see $log)"
    validate_sustained_header "$log" "$threads" "$pair" "$avx" "$miner_pin"
    parse_whole_rate "$log" >/dev/null || die "could not parse sustained rate (see $log)"
    printf '%s %s\n' "$start" "$end"
}

screen_mode() {
    local baseline= current_v3= current_v4= v4_test_binary=
    while (($#)); do
        case $1 in
            --baseline) baseline=${2:?}; shift 2 ;;
            --current-v3) current_v3=${2:?}; shift 2 ;;
            --current-v4) current_v4=${2:?}; shift 2 ;;
            --v4-test-binary) v4_test_binary=${2:?}; shift 2 ;;
            --secs) SECS=${2:?}; shift 2 ;;
            --cooldown) COOLDOWN_SECS=${2:?}; shift 2 ;;
            --out-root) OUT_ROOT=${2:?}; shift 2 ;;
            --label) LABEL=${2:?}; shift 2 ;;
            --miner-pin) MINER_PIN=true; shift ;;
            -h|--help) usage; exit 0 ;;
            *) die "unknown screen option: $1" ;;
        esac
    done
    [[ -n $baseline && -n $current_v3 ]] || die "screen requires --baseline and --current-v3"
    positive_int --secs "$SECS"
    nonnegative_int --cooldown "$COOLDOWN_SECS"
    require_binary baseline "$baseline"
    require_binary current-v3 "$current_v3"
    [[ -z $current_v4 ]] || require_binary current-v4 "$current_v4"
    [[ -z $v4_test_binary ]] || require_binary v4-test "$v4_test_binary"
    [[ -z $v4_test_binary || -n $current_v4 ]] || die "--v4-test-binary requires --current-v4"
    local v4_eligible=false
    if [[ -n $current_v4 ]] && cpu_supports_v4; then v4_eligible=true; fi
    if $v4_eligible && [[ -z $v4_test_binary ]]; then
        die "native v4 screening requires --v4-test-binary"
    fi
    if $v4_eligible; then configure_pre_block_cooldown; fi
    if $MINER_PIN; then
        warn "v0.2.18 has no Linux affinity implementation; its baseline arm will remain unpinned"
    fi

    local -a labels=(baseline-v0218-x1 current-v3-x1 current-v3-x2)
    local -a bins=("$baseline" "$current_v3" "$current_v3")
    local -a pairs=(x1 x1 x2)
    local -a avxs=(ignore false false)
    local -a pins=(false "$MINER_PIN" "$MINER_PIN")
    if [[ -n $current_v4 ]]; then
        if $v4_eligible; then
            labels+=(current-v4-x1 current-v4-x2)
            bins+=("$current_v4" "$current_v4")
            pairs+=(x1 x2)
            avxs+=(true true)
            pins+=("$MINER_PIN" "$MINER_PIN")
        else
            warn "CPU lacks the complete x86-64-v4 AVX-512 feature set; skipping current-v4"
        fi
    else
        warn "--current-v4 not supplied; screening baseline and current-v3 only"
    fi

    local max_threads=$LOGICAL_COUNT
    ((max_threads > 255)) && max_threads=255
    local -a candidates=(1 2 4 8 "$LLC_BOUNDARY" "$PHYSICAL_COUNT" "$max_threads") thread_counts=()
    local value
    mapfile -t thread_counts < <(printf '%s\n' "${candidates[@]}" | awk -v max="$max_threads" '$1 >= 1 && $1 <= max' | sort -n -u)

    start_results screen
    local threads_csv
    threads_csv=$(join_by_comma "${thread_counts[@]}")
    write_manifest screen "$SECS" "$COOLDOWN_SECS" "$threads_csv" false "$MINER_PIN"
    if [[ -n $current_v4 ]] && ! $v4_eligible; then
        record_provenance "SKIP current-v4 and v4 tests: host lacks complete x86-64-v4 feature set"
    fi
    record_binary baseline-v0218 "$baseline" baseline x1
    record_binary current-v3 "$current_v3" v3 x1+x2
    if $v4_eligible; then record_binary current-v4 "$current_v4" v4 x1+x2; fi
    if $v4_eligible && [[ -n $v4_test_binary ]]; then
        record_binary v4-tests "$v4_test_binary" v4-tests n/a
        validate_v4_build_metadata "$current_v4" "$v4_test_binary"
    fi
    if $v4_eligible; then
        validate_current_builds "$current_v3" "$current_v4"
    else
        validate_current_builds "$current_v3"
    fi
    csv_row timestamp_start timestamp_end threads cpus arm rep seconds hashes khs per_thread_hs pipeline avx512 miner_pin log >"$RUN_DIR/screen.csv"

    run_miner_version baseline-v0218 "$baseline" v0.2.18
    run_miner_selftest baseline-v0218 "$baseline"
    run_miner_version current-v3 "$current_v3"
    run_miner_selftest current-v3 "$current_v3"
    if $v4_eligible; then
        run_miner_version current-v4 "$current_v4"
        run_miner_selftest current-v4 "$current_v4"
    fi
    if $v4_eligible && [[ -n $v4_test_binary ]]; then run_v4_test_binary "$v4_test_binary"; fi
    run_pre_block_cooldown

    local warm_threads=${thread_counts[${#thread_counts[@]}-1]} warm_cpus warm_log
    warm_cpus=$(cpus_for_threads "$warm_threads")
    warm_log=$RUN_DIR/raw/warmup-current-v3-x1-t${warm_threads}.log
    printf 'warm-up (discarded): current-v3-x1, %s threads\n' "$warm_threads"
    capture_telemetry warmup-before
    run_sustained "$current_v3" "$warm_threads" "$warm_cpus" "$SECS" x1 false "$warm_log" "" "$MINER_PIN" >/dev/null
    capture_telemetry warmup-after
    ((COOLDOWN_SECS == 0)) || sleep "$COOLDOWN_SECS"

    local -a order=()
    local i threads cpus label binary pair avx miner_pin rep log start_end start end hashes khs per_thread
    for ((i=0; i<${#labels[@]}; i++)); do order+=("$i"); done
    for ((i=${#labels[@]}-1; i>=0; i--)); do order+=("$i"); done
    local -A reps=() rates=()
    for threads in "${thread_counts[@]}"; do
        cpus=$(cpus_for_threads "$threads")
        for i in "${order[@]}"; do
            label=${labels[$i]}
            binary=${bins[$i]}
            pair=${pairs[$i]}
            avx=${avxs[$i]}
            miner_pin=${pins[$i]}
            reps[$threads:$label]=$(( ${reps[$threads:$label]:-0} + 1 ))
            rep=${reps[$threads:$label]}
            log=$RUN_DIR/raw/t$(printf '%03d' "$threads")-$label-r$rep.log
            printf 'screen t=%s arm=%s rep=%s ... ' "$threads" "$label" "$rep"
            capture_telemetry "$label-t$threads-r$rep-before"
            start_end=$(run_sustained "$binary" "$threads" "$cpus" "$SECS" "$pair" "$avx" "$log" "" "$miner_pin")
            read -r start end <<<"$start_end"
            read -r hashes khs < <(parse_whole_rate "$log")
            per_thread=$(awk -v rate="$khs" -v n="$threads" 'BEGIN {printf "%.3f", rate*1000/n}')
            csv_row "$start" "$end" "$threads" "$cpus" "$label" "$rep" "$SECS" "$hashes" "$khs" "$per_thread" "$pair" "$avx" "$miner_pin" "${log#$RUN_DIR/}" >>"$RUN_DIR/screen.csv"
            rates[$label:$threads:$rep]=$khs
            capture_telemetry "$label-t$threads-r$rep-after"
            printf '%s KH/s\n' "$khs"
            ((COOLDOWN_SECS == 0)) || sleep "$COOLDOWN_SECS"
        done
    done

    csv_row arm threads median_khs per_thread_hs efficiency_vs_1t_percent >"$RUN_DIR/screen-summary.csv"
    local one_thread median efficiency
    for ((i=0; i<${#labels[@]}; i++)); do
        label=${labels[$i]}
        one_thread=$(awk -v a="${rates[$label:1:1]}" -v b="${rates[$label:1:2]}" 'BEGIN {printf "%.6f", (a+b)/2}')
        for threads in "${thread_counts[@]}"; do
            median=$(awk -v a="${rates[$label:$threads:1]}" -v b="${rates[$label:$threads:2]}" 'BEGIN {printf "%.6f", (a+b)/2}')
            per_thread=$(awk -v rate="$median" -v n="$threads" 'BEGIN {printf "%.3f", rate*1000/n}')
            efficiency=$(awk -v rate="$median" -v n="$threads" -v one="$one_thread" 'BEGIN {printf "%.3f", 100*(rate/n)/one}')
            csv_row "$label" "$threads" "$median" "$per_thread" "$efficiency" >>"$RUN_DIR/screen-summary.csv"
        done
    done
    finalize_results
}

resolve_thread_spec() {
    case $1 in
        physical) printf '%d\n' "$PHYSICAL_COUNT" ;;
        logical) printf '%d\n' "$LOGICAL_COUNT" ;;
        *) positive_int --threads "$1"; printf '%d\n' "$1" ;;
    esac
}

confirm_mode() {
    local base= candidate= v4_test_binary= base_kind=v3 candidate_kind=v4 base_pair=x1 candidate_pair=x1 thread_spec=logical
    local base_pin=false candidate_pin=false
    SECS=240
    COOLDOWN_SECS=20
    while (($#)); do
        case $1 in
            --base) base=${2:?}; shift 2 ;;
            --candidate) candidate=${2:?}; shift 2 ;;
            --base-kind) base_kind=${2:?}; shift 2 ;;
            --candidate-kind) candidate_kind=${2:?}; shift 2 ;;
            --base-pair) base_pair=${2:?}; shift 2 ;;
            --candidate-pair) candidate_pair=${2:?}; shift 2 ;;
            --v4-test-binary) v4_test_binary=${2:?}; shift 2 ;;
            --threads) thread_spec=${2:?}; shift 2 ;;
            --secs) SECS=${2:?}; shift 2 ;;
            --cooldown) COOLDOWN_SECS=${2:?}; shift 2 ;;
            --out-root) OUT_ROOT=${2:?}; shift 2 ;;
            --label) LABEL=${2:?}; shift 2 ;;
            --miner-pin) MINER_PIN=true; base_pin=true; candidate_pin=true; shift ;;
            --base-pin) base_pin=true; shift ;;
            --candidate-pin) candidate_pin=true; shift ;;
            -h|--help) usage; exit 0 ;;
            *) die "unknown confirm option: $1" ;;
        esac
    done
    [[ -n $base && -n $candidate ]] || die "confirm requires --base and --candidate"
    require_binary base "$base"
    require_binary candidate "$candidate"
    pair_bool "$base_pair" >/dev/null
    pair_bool "$candidate_pair" >/dev/null
    local base_avx candidate_avx
    base_avx=$(expected_avx512 "$base_kind")
    candidate_avx=$(expected_avx512 "$candidate_kind")
    if [[ $base_kind == baseline && $base_pin == true ]]; then
        die "v0.2.18 has no Linux affinity implementation; a baseline base arm cannot be pinned"
    fi
    if [[ $candidate_kind == baseline && $candidate_pin == true ]]; then
        die "v0.2.18 has no Linux affinity implementation; a baseline candidate arm cannot be pinned"
    fi
    positive_int --secs "$SECS"
    ((SECS >= 180)) || die "confirmation --secs must be at least 180 for steady-state checkpoints after 120s"
    ((SECS % 30 == 0)) || die "confirmation --secs must be divisible by 30 so steady-state intervals have equal weight"
    nonnegative_int --cooldown "$COOLDOWN_SECS"
    if [[ $base_kind == v4 || $candidate_kind == v4 ]]; then
        cpu_supports_v4 || die "CPU lacks the complete x86-64-v4 feature set"
        [[ -n $v4_test_binary ]] || die "v4 confirmation requires --v4-test-binary"
        require_binary v4-test "$v4_test_binary"
        configure_pre_block_cooldown
    elif [[ -n $v4_test_binary ]]; then
        die "--v4-test-binary requires a v4 confirmation arm"
    fi
    local threads
    threads=$(resolve_thread_spec "$thread_spec")
    ((threads <= LOGICAL_COUNT && threads <= 255)) || die "confirmation threads ($threads) exceed allowed/miner CPU limit"
    local cpus
    cpus=$(cpus_for_threads "$threads")

    start_results confirm
    write_manifest confirm "$SECS" "$COOLDOWN_SECS" "$threads" "$base_pin" "$candidate_pin"
    record_binary base "$base" "$base_kind" "$base_pair"
    record_binary candidate "$candidate" "$candidate_kind" "$candidate_pair"
    if [[ -n $v4_test_binary ]]; then
        record_binary v4-tests "$v4_test_binary" v4-tests n/a
        [[ $base_kind != v4 ]] || validate_v4_build_metadata "$base" "$v4_test_binary"
        [[ $candidate_kind != v4 ]] || validate_v4_build_metadata "$candidate" "$v4_test_binary"
    fi
    if [[ $base_kind == v3 && $candidate_kind == v4 ]]; then
        validate_current_builds "$base" "$candidate"
    elif [[ $base_kind == v4 && $candidate_kind == v3 ]]; then
        validate_current_builds "$candidate" "$base"
    fi
    printf 'leg,arm,steadyKHs,wholeKHs\n' >"$RUN_DIR/legs.csv"
    printf '%s\n' \
        'This archive covers one retention target only.' \
        'A decision requires a separate confirmation at 1T and at the independent screen-selected peak thread count.' \
        >"$RUN_DIR/RETENTION-NOTE.txt"
    warn "one confirmation covers one target; retention requires separate 1T and screen-selected peak-thread archives"

    if [[ $base_kind == baseline ]]; then run_miner_version base "$base" v0.2.18; else run_miner_version base "$base"; fi
    if [[ $candidate_kind == baseline ]]; then run_miner_version candidate "$candidate" v0.2.18; else run_miner_version candidate "$candidate"; fi
    run_miner_selftest base "$base"
    run_miner_selftest candidate "$candidate"
    if [[ -n $v4_test_binary ]]; then
        run_v4_test_binary "$v4_test_binary"
        run_pre_block_cooldown
    fi

    local warm_log=$RUN_DIR/raw/leg-00-warmup.log
    printf 'warm-up (discarded): base\n'
    run_sustained "$base" "$threads" "$cpus" "$SECS" "$base_pair" "$base_avx" "$warm_log" "" "$base_pin" >/dev/null
    ((COOLDOWN_SECS == 0)) || sleep "$COOLDOWN_SECS"

    local -a order=(B C C B C B B C)
    local index arm binary pair avx miner_pin log steady whole_data whole
    for ((index=1; index<=8; index++)); do
        arm=${order[$((index-1))]}
        if [[ $arm == B ]]; then
            binary=$base; pair=$base_pair; avx=$base_avx; miner_pin=$base_pin
        else
            binary=$candidate; pair=$candidate_pair; avx=$candidate_avx; miner_pin=$candidate_pin
        fi
        log=$RUN_DIR/raw/leg-$(printf '%02d' "$index")-$arm.log
        printf 'confirm leg %s/8 arm=%s ... ' "$index" "$arm"
        capture_telemetry "leg-$index-$arm-before"
        run_sustained "$binary" "$threads" "$cpus" "$SECS" "$pair" "$avx" "$log" "" "$miner_pin" >/dev/null
        steady=$(parse_steady_rate "$log") || die "no steady-state checkpoints parsed (see $log)"
        whole_data=$(parse_whole_rate "$log")
        whole=${whole_data##* }
        printf '%d,%s,%s,%s\n' "$index" "$arm" "$steady" "$whole" >>"$RUN_DIR/legs.csv"
        capture_telemetry "leg-$index-$arm-after"
        printf '%s steady KH/s\n' "$steady"
        ((index == 8 || COOLDOWN_SECS == 0)) || sleep "$COOLDOWN_SECS"
    done
    if command -v python3 >/dev/null 2>&1 && [[ -f $SCRIPT_DIR/analyze-thue-morse.py ]]; then
        python3 "$SCRIPT_DIR/analyze-thue-morse.py" "$RUN_DIR/legs.csv" >"$RUN_DIR/analysis.txt" 2>&1 || warn "Thue-Morse analysis failed; legs.csv remains available"
    fi
    finalize_results
}

profile_mode() {
    local binary= v4_test_binary= uprof= kind=v3 pair=x2
    while (($#)); do
        case $1 in
            --binary) binary=${2:?}; shift 2 ;;
            --kind) kind=${2:?}; shift 2 ;;
            --pair) pair=${2:?}; shift 2 ;;
            --v4-test-binary) v4_test_binary=${2:?}; shift 2 ;;
            --uprof) uprof=${2:?}; shift 2 ;;
            --secs) SECS=${2:?}; shift 2 ;;
            --out-root) OUT_ROOT=${2:?}; shift 2 ;;
            --label) LABEL=${2:?}; shift 2 ;;
            --miner-pin) MINER_PIN=true; shift ;;
            -h|--help) usage; exit 0 ;;
            *) die "unknown profile option: $1" ;;
        esac
    done
    [[ -n $binary ]] || die "profile requires --binary"
    require_binary profile "$binary"
    [[ -z $uprof ]] || require_binary AMD-uProf "$uprof"
    [[ $kind == v3 || $kind == v4 ]] || die "profile --kind must be v3 or v4"
    pair_bool "$pair" >/dev/null
    positive_int --secs "$SECS"
    if [[ $kind == v4 ]]; then
        cpu_supports_v4 || die "CPU lacks the complete x86-64-v4 feature set"
        [[ -n $v4_test_binary ]] || die "v4 profiling requires --v4-test-binary"
        require_binary v4-test "$v4_test_binary"
        configure_pre_block_cooldown
    elif [[ -n $v4_test_binary ]]; then
        die "--v4-test-binary requires --kind v4"
    fi
    local avx
    avx=$(expected_avx512 "$kind")

    local max_threads=$LOGICAL_COUNT
    ((max_threads > 255)) && max_threads=255
    local physical=$PHYSICAL_COUNT
    ((physical > 255)) && physical=255
    local -a profile_counts
    mapfile -t profile_counts < <(printf '%s\n' 1 "$physical" "$max_threads" | sort -n -u)
    local counts_csv
    counts_csv=$(join_by_comma "${profile_counts[@]}")
    start_results profile
    write_manifest profile "$SECS" 0 "$counts_csv"
    record_binary profile "$binary" "$kind" "$pair"
    if [[ -n $uprof ]]; then record_binary amd-uprof "$uprof" tool n/a; fi
    if [[ -n $v4_test_binary ]]; then
        record_binary v4-tests "$v4_test_binary" v4-tests n/a
        validate_v4_build_metadata "$binary" "$v4_test_binary"
    fi
    run_miner_version profile "$binary"
    run_miner_selftest profile "$binary"
    if [[ -n $v4_test_binary ]]; then
        run_v4_test_binary "$v4_test_binary"
        run_pre_block_cooldown
    fi
    mkdir -p "$RUN_DIR/profiles" "$RUN_DIR/perf" "$RUN_DIR/uprof"
    printf 'threads,cpus,pprof,perf_stat,amd_uprof\n' >"$RUN_DIR/profiles.csv"

    local perf_ok=false
    if command -v perf >/dev/null 2>&1; then
        if perf stat -o "$RUN_DIR/perf/preflight.txt" -- true >/dev/null 2>&1; then perf_ok=true; else warn "perf stat unavailable or not permitted; keeping pprof only"; fi
    else
        warn "perf not found; keeping pprof only"
    fi

    local threads cpus log profile perf_file perf_log perf_status perf_result uprof_dir uprof_log uprof_status uprof_result pair_flag
    pair_flag=$(pair_bool "$pair")
    local -a args=(--sustained --secs "$SECS" -t 0 --sa v114 "--pair=$pair_flag")
    if $MINER_PIN; then args+=(--pin); else args+=(--pin=false); fi
    for threads in "${profile_counts[@]}"; do
        cpus=$(cpus_for_threads "$threads")
        log=$RUN_DIR/raw/pprof-t${threads}.log
        profile=$RUN_DIR/profiles/cpu-t${threads}.pprof
        printf 'pprof t=%s ... ' "$threads"
        run_sustained "$binary" "$threads" "$cpus" "$SECS" "$pair" "$avx" "$log" "$profile" >/dev/null
        [[ -s $profile ]] || die "CPU profile was not written: $profile"
        printf 'done\n'
        if command -v go >/dev/null 2>&1; then
            go tool pprof -top "$binary" "$profile" >"$RUN_DIR/profiles/top-t${threads}.txt" 2>&1 || warn "go tool pprof -top failed at $threads threads"
        fi

        args[4]=$threads
        perf_result=skipped
        if $perf_ok; then
            perf_file=$RUN_DIR/perf/stat-t${threads}.csv
            perf_log=$RUN_DIR/raw/perf-t${threads}.log
            printf 'perf stat t=%s ... ' "$threads"
            set +e
            perf stat -x, -o "$perf_file" \
                -e task-clock,cycles,instructions,cache-references,cache-misses,branches,branch-misses,context-switches,cpu-migrations \
                -- taskset -c "$cpus" env "GOMAXPROCS=$threads" "$binary" "${args[@]}" >"$perf_log" 2>&1
            perf_status=$?
            set -e
            if [[ $perf_status -eq 0 ]]; then
                validate_sustained_header "$perf_log" "$threads" "$pair" "$avx"
                parse_whole_rate "$perf_log" >/dev/null || die "could not parse perf sustained run (see $perf_log)"
                perf_result=${perf_file#$RUN_DIR/}
                printf 'done\n'
            else
                perf_result="failed:$perf_status"
                warn "perf stat failed at $threads threads; see $perf_log"
            fi
        fi

        uprof_result=skipped
        if [[ -n $uprof ]]; then
            uprof_dir=$RUN_DIR/uprof/t${threads}
            uprof_log=$RUN_DIR/raw/uprof-t${threads}.log
            mkdir -p "$uprof_dir"
            printf 'AMD uProf t=%s ... ' "$threads"
            set +e
            "$uprof" collect --config hotspots -g -o "$uprof_dir" \
                taskset -c "$cpus" env "GOMAXPROCS=$threads" "$binary" "${args[@]}" >"$uprof_log" 2>&1
            uprof_status=$?
            set -e
            if [[ $uprof_status -eq 0 ]]; then
                find "$uprof_dir" -mindepth 1 -print -quit | grep -q . || die "AMD uProf wrote no profile data (see $uprof_log)"
                validate_sustained_header "$uprof_log" "$threads" "$pair" "$avx"
                parse_whole_rate "$uprof_log" >/dev/null || die "could not parse AMD uProf sustained run (see $uprof_log)"
                uprof_result=${uprof_dir#$RUN_DIR/}
                printf 'done\n'
            else
                uprof_result="failed:$uprof_status"
                warn "AMD uProf failed at $threads threads; see $uprof_log"
            fi
        fi
        csv_row "$threads" "$cpus" "${profile#$RUN_DIR/}" "$perf_result" "$uprof_result" >>"$RUN_DIR/profiles.csv"
    done
    finalize_results
}

self_test() {
    local got temp log gate whole steady
    PRE_BLOCK_COOLDOWN_SECS=0
    run_pre_block_cooldown
    got=$(expand_cpulist '0-2,5,8-9' | paste -sd, -)
    [[ $got == 0,1,2,5,8,9 ]] || die "self-test cpulist failed: $got"
    temp=$(mktemp -d)
    log=$temp/sustained.log
    trap 'rm -rf -- "$temp"' RETURN
    cat >"$log" <<'EOF'
go-miner test sustained bench: 4 threads, 180s, pin=false, sa=v114, avx512=false, pipeline=x2
120+  t=2m0s interval=   10.00 KH/s total=100
120+  t=2m30s interval=   20.00 KH/s total=200
120+  t=3m0s interval=   22.00 KH/s total=300
300 hashes in 3m0s = 21.00 KH/s (5250.0 H/s/thread)
EOF
    validate_sustained_header "$log" 4 x2 false
    whole=$(parse_whole_rate "$log")
    [[ $whole == '300 21.000000' ]] || die "self-test whole-rate parser failed: $whole"
    steady=$(parse_steady_rate "$log")
    [[ $steady == 21.000000 ]] || die "self-test steady-rate parser failed: $steady"
    gate=$temp/gate.log
    printf 'V114 GATE: 1000008/1000008 hashes matched, 0 fallbacks\n' >"$gate"
    gate_reports_zero_fallbacks "$gate" || die "self-test zero-fallback gate rejected valid output"
    printf 'V114 GATE: 1000008/1000008 hashes matched, 10 fallbacks\n' >"$gate"
    if gate_reports_zero_fallbacks "$gate"; then die "self-test zero-fallback gate accepted 10 fallbacks"; fi
    printf 'self-test ok: topology/header/rate parsers and exact zero-fallback gate\n'
}

main() {
    (($#)) || { usage; exit 1; }
    if [[ $1 == --self-test ]]; then self_test; exit 0; fi
    [[ $(uname -s) == Linux ]] || die "$SCRIPT_NAME supports Linux/HiveOS only"
    need_command awk
    need_command find
    need_command grep
    need_command sort
    need_command tar
    need_command taskset
    discover_topology
    MODE=$1
    shift
    case $MODE in
        screen) screen_mode "$@" ;;
        confirm) confirm_mode "$@" ;;
        profile) profile_mode "$@" ;;
        -h|--help) usage ;;
        *) die "unknown mode: $MODE" ;;
    esac
}

main "$@"
