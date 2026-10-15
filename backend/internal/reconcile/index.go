package reconcile

// maxIndex only visits branches whose maximum balance can cover a cancellation.
// At most two witnesses are needed to reject an ambiguous automatic decision.
type maxIndex struct {
	n    int
	tree []int64
}

func newIndex(size int) *maxIndex {
	n := 1
	for n < size {
		n *= 2
	}
	return &maxIndex{n: n, tree: make([]int64, 2*n)}
}
func (x *maxIndex) set(i int, value int64) {
	i += x.n
	x.tree[i] = value
	for i /= 2; i > 0; i /= 2 {
		x.tree[i] = max(x.tree[2*i], x.tree[2*i+1])
	}
}
func (x *maxIndex) find(quantity int64) []int {
	found := make([]int, 0, 2)
	var visit func(int)
	visit = func(node int) {
		if len(found) == 2 || x.tree[node] < quantity {
			return
		}
		if node >= x.n {
			found = append(found, node-x.n)
			return
		}
		visit(node * 2)
		visit(node*2 + 1)
	}
	visit(1)
	return found
}
