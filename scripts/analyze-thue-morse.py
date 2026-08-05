#!/usr/bin/env python3
"""Drift-adjusted treatment estimate for a bench-thue-morse legs.csv.

Fits ln(rate) = b0 + b1*x + b2*x^2 + d*T over the 8 measured legs (x = leg
position 1..8, T = 1 for candidate legs). The Thue-Morse order keeps T
orthogonal to both drift terms, so d is identified with 4 residual degrees of
freedom. Reports the percent effect exp(d)-1, its standard error, the two-sided
95% CI, and the one-sided 95% lower bound the retention gate uses
(t crit 2.776 / 2.132 at df=4).

Usage: python analyze-thue-morse.py path/to/legs.csv
"""

import csv
import math
import sys

T_TWO_SIDED_95 = {1: 12.706, 2: 4.303, 3: 3.182, 4: 2.776, 5: 2.571, 6: 2.447}
T_ONE_SIDED_95 = {1: 6.314, 2: 2.920, 3: 2.353, 4: 2.132, 5: 2.015, 6: 1.943}


def solve(a, b):
    """Gaussian elimination with partial pivoting; a is n x n, b length n."""
    n = len(a)
    m = [row[:] + [b[i]] for i, row in enumerate(a)]
    for col in range(n):
        piv = max(range(col, n), key=lambda r: abs(m[r][col]))
        if abs(m[piv][col]) < 1e-12:
            raise SystemExit("singular design matrix")
        m[col], m[piv] = m[piv], m[col]
        for r in range(n):
            if r != col:
                f = m[r][col] / m[col][col]
                for c in range(col, n + 1):
                    m[r][c] -= f * m[col][c]
    return [m[i][n] / m[i][i] for i in range(n)]


def invert(a):
    n = len(a)
    cols = []
    for j in range(n):
        e = [1.0 if i == j else 0.0 for i in range(n)]
        cols.append(solve(a, e))
    return [[cols[j][i] for j in range(n)] for i in range(n)]


def main(path):
    rows = []
    with open(path, newline="") as f:
        for row in csv.DictReader(f):
            arm = row["arm"].strip().strip('"')
            if arm in ("B", "C"):
                rows.append((int(row["leg"]), arm, float(row["steadyKHs"])))
    rows.sort()
    if len(rows) != 8:
        raise SystemExit(f"expected 8 measured legs, found {len(rows)}")

    xs = [[1.0, leg, leg * leg, 1.0 if arm == "C" else 0.0] for leg, arm, _ in rows]
    ys = [math.log(rate) for _, _, rate in rows]

    p = 4
    xtx = [[sum(xs[r][i] * xs[r][j] for r in range(8)) for j in range(p)] for i in range(p)]
    xty = [sum(xs[r][i] * ys[r] for r in range(8)) for i in range(p)]
    beta = solve(xtx, xty)

    resid = [ys[r] - sum(beta[i] * xs[r][i] for i in range(p)) for r in range(8)]
    df = 8 - p
    s2 = sum(e * e for e in resid) / df
    cov = invert(xtx)
    se_d = math.sqrt(s2 * cov[3][3])
    d = beta[3]

    pct = lambda v: 100.0 * (math.exp(v) - 1.0)
    two = T_TWO_SIDED_95[df]
    one = T_ONE_SIDED_95[df]

    print(f"legs (position, arm, steady KH/s):")
    for leg, arm, rate in rows:
        print(f"  {leg}  {arm}  {rate:.4f}")
    print(f"\ntreatment (candidate vs base), drift-adjusted, df={df}:")
    print(f"  point estimate : {pct(d):+.3f}%")
    print(f"  standard error : {pct(se_d) - 0:.3f} pp (log-scale se {se_d:.5f})")
    print(f"  95% CI         : [{pct(d - two * se_d):+.3f}%, {pct(d + two * se_d):+.3f}%]")
    print(f"  one-sided 95% lower bound: {pct(d - one * se_d):+.3f}%")
    print(f"  linear drift   : {pct(beta[1]):+.3f}%/leg, quadratic: {pct(beta[2]):+.4f}%/leg^2")
    print("\nretention needs: point >= +2% AND one-sided lower bound > 0 at the primary target.")


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit(__doc__)
    main(sys.argv[1])
