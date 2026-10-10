package analysis

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strings"
)

// WriteDOT writes the call graph of the functions as a Graphviz digraph
// (FW-7): a node per function, named by its label and address, an edge per
// call. The labels given override the ones of the analysis (the user's
// names); a target called that is not a function found gets a node too.
func (r *Result) WriteDOT(w io.Writer, labels ...map[uint64]string) error {
	name := func(addr uint64) string {
		for i := len(labels) - 1; i >= 0; i-- {
			if l := labels[i][addr]; l != "" {
				return l
			}
		}
		return r.Labels[addr]
	}
	bw := bufio.NewWriter(w)
	fmt.Fprintln(bw, "digraph calls {")
	fmt.Fprintln(bw, "\trankdir=LR;")
	fmt.Fprintln(bw, "\tnode [shape=box, fontname=\"monospace\"];")
	funcs := r.SortedFuncs()
	nodes := make(map[uint64]bool, len(funcs))
	node := func(addr uint64, external bool) {
		if nodes[addr] {
			return
		}
		nodes[addr] = true
		label := fmt.Sprintf("0x%X", addr)
		if n := name(addr); n != "" {
			label = n + "\\n" + label
		}
		attrs := ""
		if external {
			attrs = ", style=dashed"
		}
		fmt.Fprintf(bw, "\tf_%X [label=\"%s\"%s];\n", addr, dotEscape(label), attrs)
	}
	for _, f := range funcs {
		node(f.Addr, false)
	}
	for _, f := range funcs {
		for _, c := range f.Calls {
			if _, ok := r.Funcs[c]; !ok {
				node(c, true)
			}
		}
	}
	for _, f := range funcs {
		calls := append([]uint64(nil), f.Calls...)
		sort.Slice(calls, func(i, j int) bool { return calls[i] < calls[j] })
		for _, c := range calls {
			fmt.Fprintf(bw, "\tf_%X -> f_%X;\n", f.Addr, c)
		}
	}
	fmt.Fprintln(bw, "}")
	return bw.Flush()
}

// dotEscape quotes a label: the "\n" put in by WriteDOT stays a line break
func dotEscape(s string) string {
	s = strings.ReplaceAll(s, `"`, `\"`)
	return strings.ReplaceAll(s, "\n", `\n`)
}

// CallNode is a node of a tree of callers or callees
type CallNode struct {
	Addr uint64
	// Cycle: the function is already on the path from the root (recursion);
	// its children are not listed
	Cycle bool
}

// CallChildren returns the functions a function calls (or the ones calling
// it when callers), sorted by address, each marked when it is on the path
// from the root already: the path is the addresses from the root down to
// the function, the function included
func (r *Result) CallChildren(path []uint64, callers bool) []CallNode {
	if len(path) == 0 {
		return nil
	}
	f, ok := r.Funcs[path[len(path)-1]]
	if !ok {
		return nil
	}
	next := f.Calls
	if callers {
		next = f.Callers
	}
	out := make([]CallNode, 0, len(next))
	for _, a := range next {
		cycle := false
		for _, p := range path {
			if p == a {
				cycle = true
				break
			}
		}
		out = append(out, CallNode{Addr: a, Cycle: cycle})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Addr < out[j].Addr })
	return out
}
