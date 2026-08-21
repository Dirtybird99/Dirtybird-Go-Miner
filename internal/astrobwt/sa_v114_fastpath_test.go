package astrobwt

import "testing"

func TestV114FastPaths(t *testing.T) {
	t.Logf("AVX512MiningPath=%v", AVX512MiningPath)

	t.Run("equal columns", func(t *testing.T) {
		data := make([]byte, 3<<8)
		for i := 0; i < 1<<8; i++ {
			data[i], data[1<<8+i], data[2<<8+i] = byte(i), byte(i), byte(i)
		}
		for _, column := range []int{1, 65, 130, 255} {
			data[1<<8+column]++
		}

		var got [4]uint64
		buildEqualColumns(&data[0], 3, &got)
		for column := 0; column < 1<<8; column++ {
			want := column != 1 && column != 65 && column != 130 && column != 255
			equal := got[column>>6]&(uint64(1)<<uint(column&63)) != 0
			if equal != want {
				t.Fatalf("column %d equal=%v, want %v", column, equal, want)
			}
		}
	})

	t.Run("materialize padded boundary", func(t *testing.T) {
		v := newV114Scratch()
		if cap(v.order) < stage4MaxGroupRun+8 || cap(v.arena) < arenaIndexCount+8 {
			t.Fatalf("scratch is not padded: order=%d arena=%d", cap(v.order), cap(v.arena))
		}
		order := v.order[:stage4MaxGroupRun]
		for i := range order {
			order[i] = uint32(i * 256)
		}
		arena := v.arena[:arenaIndexCount]
		const count, rel = uint32(5), uint32(17)
		src := stage4MaxGroupRun - int(count)
		dst := arenaIndexCount - int(count)
		materializeOrigins(&arena[dst], &order[src], count, rel)
		for i := 0; i < int(count); i++ {
			if got, want := arena[dst+i], order[src+i]+rel; got != want {
				t.Fatalf("arena[%d]=%d, want %d", dst+i, got, want)
			}
		}
	})

	t.Run("unique run batch", func(t *testing.T) {
		if !uniqueRunBatchAvailable {
			t.Skip("requires GOAMD64=v3")
		}
		v := newV114Scratch()
		arena := v.arena[:cap(v.arena)]
		begin := arenaIndexCount - 5
		for i := 0; i < 5; i++ {
			arena[begin+i] = uint32(100 + i)
		}
		sa := make([]uint32, 24)
		runs := []stage5Run{
			{key: 1, packed: 7},
			{key: 2, packed: 5<<17 | uint32(begin)},
			{key: 3, packed: 9 << 17},
		}
		group, out := writeUniqueRunBatch(&sa[0], &runs[0], &arena[0], len(runs), 0, 0, 16, cap(arena))
		if group != 2 || out != 6 {
			t.Fatalf("unique batch stopped at (%d,%d), want (2,6)", group, out)
		}
		want := []uint32{7, 100, 101, 102, 103, 104}
		for i := range want {
			if sa[i] != want[i] {
				t.Fatalf("sa[%d]=%d, want %d", i, sa[i], want[i])
			}
		}

		collision := []stage5Run{{key: 4, packed: 1}, {key: 4, packed: 2}}
		if g, o := writeUniqueRunBatch(&sa[0], &collision[0], &arena[0], 2, 0, 0, 16, cap(arena)); g != 0 || o != 0 {
			t.Fatalf("collision advanced to (%d,%d)", g, o)
		}
		literal := []stage5Run{{key: 5, packed: 1}}
		if g, o := writeUniqueRunBatch(&sa[0], &literal[0], &arena[0], 1, 0, 1, 1, cap(arena)); g != 0 || o != 1 {
			t.Fatalf("logical bound advanced to (%d,%d)", g, o)
		}
		short := []stage5Run{{key: 6, packed: 4 << 17}}
		if g, o := writeUniqueRunBatch(&sa[0], &short[0], &arena[0], 1, 0, 0, 16, 7); g != 0 || o != 0 {
			t.Fatalf("arena bound advanced to (%d,%d)", g, o)
		}
	})
}
