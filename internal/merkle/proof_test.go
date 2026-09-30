package merkle

import "testing"

func buildTree(n int) []Hash {
	leaves := make([]Hash, n)
	for i := range leaves {
		leaves[i] = Leaf([]byte{byte(i), byte(i >> 8)})
	}
	return leaves
}

func TestProofRoundTripsForEveryIndex(t *testing.T) {
	for _, n := range []int{1, 2, 4, 8, 16, 64} {
		leaves := buildTree(n)
		root := Root(leaves)
		for i := 0; i < n; i++ {
			proof := Proof(leaves, i)
			if !VerifyProof(root, leaves[i], i, n, proof) {
				t.Errorf("n=%d index=%d: proof did not verify", n, i)
			}
		}
	}
}

func TestVerifyProofRejectsWrongLeaf(t *testing.T) {
	leaves := buildTree(8)
	root := Root(leaves)
	proof := Proof(leaves, 3)
	wrongLeaf := Leaf([]byte("not the real leaf"))
	if VerifyProof(root, wrongLeaf, 3, 8, proof) {
		t.Fatal("VerifyProof accepted a leaf that was never part of the tree")
	}
}

func TestVerifyProofRejectsWrongIndex(t *testing.T) {
	leaves := buildTree(8)
	root := Root(leaves)
	proof := Proof(leaves, 3)
	// The same proof, claimed against a different index, must fail - the
	// index determines left/right ordering at every combine step.
	if VerifyProof(root, leaves[3], 5, 8, proof) {
		t.Fatal("VerifyProof accepted the right leaf at the wrong claimed index")
	}
}

func TestVerifyProofRejectsTamperedProofHash(t *testing.T) {
	leaves := buildTree(8)
	root := Root(leaves)
	proof := Proof(leaves, 3)
	proof[0] = Leaf([]byte("tampered"))
	if VerifyProof(root, leaves[3], 3, 8, proof) {
		t.Fatal("VerifyProof accepted a tampered proof hash")
	}
}

func TestVerifyProofRejectsWrongRoot(t *testing.T) {
	leaves := buildTree(8)
	proof := Proof(leaves, 3)
	wrongRoot := Leaf([]byte("not the real root"))
	if VerifyProof(wrongRoot, leaves[3], 3, 8, proof) {
		t.Fatal("VerifyProof accepted a proof against the wrong root")
	}
}

func TestVerifyProofRejectsMalformedInputs(t *testing.T) {
	leaves := buildTree(8)
	root := Root(leaves)
	proof := Proof(leaves, 3)

	if VerifyProof(root, leaves[3], 3, 7, proof) {
		t.Fatal("VerifyProof accepted a non-power-of-two totalLeaves")
	}
	if VerifyProof(root, leaves[3], 8, 8, proof) {
		t.Fatal("VerifyProof accepted an out-of-range index")
	}
	if VerifyProof(root, leaves[3], 3, 8, proof[:len(proof)-1]) {
		t.Fatal("VerifyProof accepted a proof with the wrong length")
	}
}

func TestProofPanicsOnBadInputs(t *testing.T) {
	leaves := buildTree(8)
	mustPanic := func(name string, f func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s did not panic", name)
			}
		}()
		f()
	}
	mustPanic("non-power-of-two leaves", func() { Proof(buildTree(7), 0) })
	mustPanic("negative index", func() { Proof(leaves, -1) })
	mustPanic("out-of-range index", func() { Proof(leaves, 8) })
}
