package engine

// joinOutputLink records one joined output token produced by a positive join
// node for a (left row, right row) pair. Links are doubly chained per
// producing row, anchored at betaTokenRow.outputHead, so removing an input
// row reaches its stored outputs without reconstructing tokens, and the
// surviving opposite row's chain emptiness answers whether it still has any
// match. Refs are 1-based indices into the owning betaJoinBucketTable arenas.
type joinOutputLink struct {
	output    tokenRef
	leftRef   int32
	rightRef  int32
	leftNext  int32
	leftPrev  int32
	rightNext int32
	rightPrev int32
}

// joinOutputTable is a flat link arena with a freelist threaded through
// leftNext. The table holds a durable retain on each stored output tip row,
// released when the link is freed.
type joinOutputTable struct {
	links    []joinOutputLink
	freeHead int32
	count    int
}

func (t *joinOutputTable) link(left *betaJoinBucketTable, leftRef int32, right *betaJoinBucketTable, rightRef int32, output tokenRef) {
	if t == nil || output.isZero() || leftRef <= 0 || rightRef <= 0 {
		return
	}
	var ref int32
	if t.freeHead != 0 {
		ref = t.freeHead
		t.freeHead = t.links[ref-1].leftNext
	} else {
		t.links = append(t.links, joinOutputLink{})
		ref = int32(len(t.links))
	}
	leftHead := &left.rows[leftRef-1].outputHead
	rightHead := &right.rows[rightRef-1].outputHead
	link := &t.links[ref-1]
	*link = joinOutputLink{
		output:    output,
		leftRef:   leftRef,
		rightRef:  rightRef,
		leftNext:  *leftHead,
		rightNext: *rightHead,
	}
	if link.leftNext != 0 {
		t.links[link.leftNext-1].leftPrev = ref
	}
	if link.rightNext != 0 {
		t.links[link.rightNext-1].rightPrev = ref
	}
	*leftHead = ref
	*rightHead = ref
	t.count++
	output.retain()
}

func (t *joinOutputTable) unlinkFromLeft(ref int32, left *betaJoinBucketTable) {
	link := &t.links[ref-1]
	if link.leftPrev == 0 {
		if link.leftRef > 0 && int(link.leftRef) <= len(left.rows) {
			left.rows[link.leftRef-1].outputHead = link.leftNext
		}
	} else {
		t.links[link.leftPrev-1].leftNext = link.leftNext
	}
	if link.leftNext != 0 {
		t.links[link.leftNext-1].leftPrev = link.leftPrev
	}
}

func (t *joinOutputTable) unlinkFromRight(ref int32, right *betaJoinBucketTable) {
	link := &t.links[ref-1]
	if link.rightPrev == 0 {
		if link.rightRef > 0 && int(link.rightRef) <= len(right.rows) {
			right.rows[link.rightRef-1].outputHead = link.rightNext
		}
	} else {
		t.links[link.rightPrev-1].rightNext = link.rightNext
	}
	if link.rightNext != 0 {
		t.links[link.rightNext-1].rightPrev = link.rightPrev
	}
}

func (t *joinOutputTable) free(ref int32) {
	t.links[ref-1] = joinOutputLink{leftNext: t.freeHead}
	t.freeHead = ref
	t.count--
}

// removeAllForLeft drains the removed left row's chain. Per link it unlinks
// from the surviving right row's chain first, so fn observes post-removal
// emptiness, then releases the stored output after fn has propagated it. The
// left row is already out of its table; its chain head arrives via the
// removed row copy.
func (t *joinOutputTable) removeAllForLeft(head int32, right *betaJoinBucketTable, fn func(output tokenRef, rightRef int32, rightEmpty bool)) {
	if t == nil {
		return
	}
	for ref := head; ref != 0; {
		link := &t.links[ref-1]
		next := link.leftNext
		output, rightRef := link.output, link.rightRef
		t.unlinkFromRight(ref, right)
		rightEmpty := rightRef > 0 && int(rightRef) <= len(right.rows) && right.rows[rightRef-1].outputHead == 0
		if fn != nil {
			fn(output, rightRef, rightEmpty)
		}
		output.release()
		t.free(ref)
		ref = next
	}
}

func (t *joinOutputTable) removeAllForRight(head int32, left *betaJoinBucketTable, fn func(output tokenRef, leftRef int32, leftEmpty bool)) {
	if t == nil {
		return
	}
	for ref := head; ref != 0; {
		link := &t.links[ref-1]
		next := link.rightNext
		output, leftRef := link.output, link.leftRef
		t.unlinkFromLeft(ref, left)
		leftEmpty := leftRef > 0 && int(leftRef) <= len(left.rows) && left.rows[leftRef-1].outputHead == 0
		if fn != nil {
			fn(output, leftRef, leftEmpty)
		}
		output.release()
		t.free(ref)
		ref = next
	}
}

// clear drops all links without releasing stored outputs; it is only valid
// alongside a token arena reset, matching betaJoinBucketTable.clear.
func (t *joinOutputTable) clear() {
	if t == nil {
		return
	}
	clear(t.links)
	t.links = t.links[:0]
	t.freeHead = 0
	t.count = 0
}

func (t *joinOutputTable) len() int {
	if t == nil {
		return 0
	}
	return t.count
}
