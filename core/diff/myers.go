package diff

// op is a step of an edit script: n equal bytes, or a deleted or inserted byte
type op struct {
	kind Kind
	n    int
}

// myers finds the shortest edit script of x into y (E. Myers, "An O(ND)
// difference algorithm", 1986). The state of every step is kept for going
// back, which is O(D²) of memory, so it gives up (false) when the distance is
// over maxD; then the caller compares the bytes at the same offsets. The
// states go to *scratch, which is reused between calls.
func myers(x, y []byte, maxD int, scratch *[]int) ([]op, bool) {
	n, m := len(x), len(y)
	maxD = min(maxD, n+m)
	off := maxD + 1
	v := make([]int, 2*maxD+3)
	// the state before step d is trace[d*d+2*d:][:2*d+3]
	trace := (*scratch)[:0]
	defer func() { *scratch = trace }()
	for d := 0; d <= maxD; d++ {
		trace = append(trace, v[off-d-1:off+d+2]...)
		for k := -d; k <= d; k += 2 {
			var xi int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				xi = v[off+k+1]
			} else {
				xi = v[off+k-1] + 1
			}
			yi := xi - k
			for xi < n && yi < m && x[xi] == y[yi] {
				xi++
				yi++
			}
			v[off+k] = xi
			if xi >= n && yi >= m {
				return backtrack(trace, n, m, d), true
			}
		}
	}
	return nil, false
}

// backtrack walks the saved states from the end to the start
func backtrack(trace []int, n, m, dEnd int) []op {
	var rev []op
	xi, yi := n, m
	for d := dEnd; d > 0; d-- {
		vp := trace[d*d+2*d:] // the state before step d; index k is vp[k+d+1]
		at := func(k int) int { return vp[k+d+1] }
		k := xi - yi
		var pk int
		if k == -d || (k != d && at(k-1) < at(k+1)) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px := at(pk)
		py := px - pk
		sx := px // where the snake starts after the edit
		if pk == k-1 {
			sx = px + 1
		}
		if xi > sx {
			rev = append(rev, op{Equal, xi - sx})
		}
		if pk == k+1 {
			rev = append(rev, op{Inserted, 1})
		} else {
			rev = append(rev, op{Deleted, 1})
		}
		xi, yi = px, py
	}
	if xi > 0 {
		rev = append(rev, op{Equal, xi})
	}
	ops := make([]op, 0, len(rev))
	for i := len(rev) - 1; i >= 0; i-- {
		ops = append(ops, rev[i])
	}
	return ops
}
