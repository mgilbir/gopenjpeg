// Package tgt ports tgt.c/tgt.h: the tag-tree coder used by tier-2 packet
// header coding. A tag tree is a hierarchical structure over a 2-D array of
// leaves used to code, incrementally and up to a threshold, non-decreasing
// integer values (inclusion information and zero-bit-plane counts).
//
// The C code links nodes with parent pointers into a flat calloc'd array. This
// port keeps the flat node slice but represents the parent link as an index
// (with -1 meaning "no parent"), which is equivalent to a NULL pointer and
// avoids any dependence on slice-backing-array stability.
package tgt

import (
	"errors"

	"github.com/mgilbir/gopenjpeg/internal/bio"
	"github.com/mgilbir/gopenjpeg/internal/event"
)

// noParent represents a NULL parent pointer (0 in the C code).
const noParent = -1

// node ports opj_tgt_node_t.
type node struct {
	parent int   // index into Tree.nodes, or noParent
	value  int32 // opj_tgt_node_t.value
	low    int32 // opj_tgt_node_t.low
	known  uint32
}

// Tree ports opj_tgt_tree_t.
type Tree struct {
	numLeafsH uint32
	numLeafsV uint32
	numNodes  uint32
	nodes     []node
	// nodesSize ports nodes_size: the maximum capacity (in nodes) ever
	// allocated, so Init only reallocates when it must grow, matching the C
	// realloc guard.
	nodesSize uint32
}

// ErrTooSmall is returned when the leaf dimensions collapse to a tree with no
// nodes (numnodes == 0), matching the C code's "return 00" for that case.
var ErrTooSmall = errors.New("tgt: tag-tree has no nodes")

// buildLinks performs the level/parent-linking computation shared by
// opj_tgt_create and opj_tgt_init. It sets t.numNodes and (re)links the
// parents of all nodes. It returns ErrTooSmall when numnodes == 0.
//
// It reproduces the C parenting loop exactly, using node indices in place of
// pointers.
func (t *Tree) computeNumNodes(numLeafsH, numLeafsV uint32) (nplh, nplv [32]int32, numLevels uint32, err error) {
	nplh[0] = int32(numLeafsH)
	nplv[0] = int32(numLeafsV)
	t.numNodes = 0
	var n uint32
	for {
		// n = nplh[numLevels] * nplv[numLevels] as OPJ_INT32 multiply,
		// reinterpreted as OPJ_UINT32.
		n = uint32(nplh[numLevels] * nplv[numLevels])
		nplh[numLevels+1] = (nplh[numLevels] + 1) / 2
		nplv[numLevels+1] = (nplv[numLevels] + 1) / 2
		t.numNodes += n
		numLevels++
		if n <= 1 {
			break
		}
	}

	if t.numNodes == 0 {
		return nplh, nplv, numLevels, ErrTooSmall
	}
	return nplh, nplv, numLevels, nil
}

// linkParents reproduces the C parent-assignment loop for both create and init.
func (t *Tree) linkParents(nplh, nplv [32]int32, numLevels uint32) {
	nodeIdx := 0
	parentIdx := int(t.numLeafsH * t.numLeafsV)
	parent0Idx := parentIdx

	for i := uint32(0); i < numLevels-1; i++ {
		for j := int32(0); j < nplv[i]; j++ {
			k := nplh[i]
			for {
				k--
				if k < 0 {
					break
				}
				t.nodes[nodeIdx].parent = parentIdx
				nodeIdx++
				k--
				if k >= 0 {
					t.nodes[nodeIdx].parent = parentIdx
					nodeIdx++
				}
				parentIdx++
			}
			if (j&1) != 0 || j == nplv[i]-1 {
				parent0Idx = parentIdx
			} else {
				parentIdx = parent0Idx
				parent0Idx += int(nplh[i])
			}
		}
	}
	t.nodes[nodeIdx].parent = noParent
}

// Create ports opj_tgt_create: build a tag tree over a numLeafsH x numLeafsV
// array of leaves. It returns ErrTooSmall (the C "return 00" case) when the
// dimensions yield no nodes. The event manager is accepted to mirror the C
// signature; it is currently unused because Go allocation failures panic
// rather than returning NULL.
func Create(numLeafsH, numLeafsV uint32, mgr *event.Manager) (*Tree, error) {
	t := &Tree{numLeafsH: numLeafsH, numLeafsV: numLeafsV}

	nplh, nplv, numLevels, err := t.computeNumNodes(numLeafsH, numLeafsV)
	if err != nil {
		return nil, err
	}

	t.nodes = make([]node, t.numNodes)
	t.nodesSize = t.numNodes // measured in nodes (C measures in bytes)

	t.linkParents(nplh, nplv, numLevels)
	t.Reset()
	return t, nil
}

// Init ports opj_tgt_init: reinitialise an existing tree for new leaf
// dimensions, only reallocating the node slice when it must grow (matching the
// C nodes_size guard). It resets the tree and returns it, or ErrTooSmall when
// numnodes collapses to zero.
func (t *Tree) Init(numLeafsH, numLeafsV uint32, mgr *event.Manager) error {
	if t.numLeafsH != numLeafsH || t.numLeafsV != numLeafsV {
		t.numLeafsH = numLeafsH
		t.numLeafsV = numLeafsV

		nplh, nplv, numLevels, err := t.computeNumNodes(numLeafsH, numLeafsV)
		if err != nil {
			// C calls opj_tgt_destroy here; the Go caller discards the tree.
			return err
		}

		if t.numNodes > t.nodesSize {
			// Grow, preserving existing contents like realloc; the tail is
			// zero-filled by make, matching the C memset of the new region.
			newNodes := make([]node, t.numNodes)
			copy(newNodes, t.nodes)
			t.nodes = newNodes
			t.nodesSize = t.numNodes
		}

		t.linkParents(nplh, nplv, numLevels)
	}
	t.Reset()
	return nil
}

// Reset ports opj_tgt_reset: set every node's value to 999 and clear low/known.
func (t *Tree) Reset() {
	for i := uint32(0); i < t.numNodes; i++ {
		t.nodes[i].value = 999
		t.nodes[i].low = 0
		t.nodes[i].known = 0
	}
}

// SetValue ports opj_tgt_setvalue: propagate value up the tree, lowering each
// ancestor's value while it exceeds value.
func (t *Tree) SetValue(leafno uint32, value int32) {
	idx := int(leafno)
	for idx != noParent && t.nodes[idx].value > value {
		t.nodes[idx].value = value
		idx = t.nodes[idx].parent
	}
}

// Encode ports opj_tgt_encode: emit, into bio, the bits needed to code the
// value of leaf leafno up to (but not including) threshold.
func (t *Tree) Encode(b *bio.BIO, leafno uint32, threshold int32) {
	// Stack of node indices from the leaf up to (excluding) the root.
	var stk []int
	idx := int(leafno)
	for t.nodes[idx].parent != noParent {
		stk = append(stk, idx)
		idx = t.nodes[idx].parent
	}

	var low int32
	for {
		if low > t.nodes[idx].low {
			t.nodes[idx].low = low
		} else {
			low = t.nodes[idx].low
		}

		for low < threshold {
			if low >= t.nodes[idx].value {
				if t.nodes[idx].known == 0 {
					b.PutBit(1)
					t.nodes[idx].known = 1
				}
				break
			}
			b.PutBit(0)
			low++
		}

		t.nodes[idx].low = low
		if len(stk) == 0 {
			break
		}
		idx = stk[len(stk)-1]
		stk = stk[:len(stk)-1]
	}
}

// Decode ports opj_tgt_decode: read from bio the bits coding leaf leafno up to
// threshold. It returns 1 if the decoded leaf value is < threshold, 0
// otherwise, exactly matching the C return semantics.
func (t *Tree) Decode(b *bio.BIO, leafno uint32, threshold int32) uint32 {
	var stk []int
	idx := int(leafno)
	for t.nodes[idx].parent != noParent {
		stk = append(stk, idx)
		idx = t.nodes[idx].parent
	}

	var low int32
	for {
		if low > t.nodes[idx].low {
			t.nodes[idx].low = low
		} else {
			low = t.nodes[idx].low
		}
		for low < threshold && low < t.nodes[idx].value {
			if b.Read(1) != 0 {
				t.nodes[idx].value = low
			} else {
				low++
			}
		}
		t.nodes[idx].low = low
		if len(stk) == 0 {
			break
		}
		idx = stk[len(stk)-1]
		stk = stk[:len(stk)-1]
	}

	if t.nodes[idx].value < threshold {
		return 1
	}
	return 0
}
