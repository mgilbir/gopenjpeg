package mct

// This file is a faithful port of invert.c: LU-decomposition based inversion
// of a square float32 matrix, used to invert custom MCT matrices. All
// arithmetic is float32 to match the C reference bit-for-bit.

// MatrixInversionF is a port of opj_matrix_inversion_f. It inverts the nbComp x
// nbComp row-major matrix src into dest. src is modified in place (it receives
// the LU decomposition). It returns false if the matrix is singular.
func MatrixInversionF(src, dest []float32, nbComp uint32) bool {
	permutations := make([]uint32, nbComp)
	swapArea := make([]float32, nbComp)

	if !lupDecompose(src, permutations, swapArea, nbComp) {
		return false
	}

	srcTemp := make([]float32, nbComp)
	destTemp := make([]float32, nbComp)
	lupInvert(src, dest, nbComp, permutations, srcTemp, destTemp, swapArea)
	return true
}

// lupDecompose is a port of opj_lupDecompose. It performs an in-place LU
// decomposition of the row-major matrix with partial (row) pivoting, recording
// the pivot order in permutations. p_swap_area is scratch used for row swaps.
func lupDecompose(matrix []float32, permutations []uint32, pSwapArea []float32, nbCompo uint32) bool {
	for i := uint32(0); i < nbCompo; i++ {
		permutations[i] = i
	}

	lLastColum := nbCompo - 1
	var k2 uint32
	for k := uint32(0); k < lLastColum; k++ {
		var p float32

		// Find the pivot: biggest magnitude in column k, rows k..nbCompo-1.
		for i := k; i < nbCompo; i++ {
			v := matrix[i*nbCompo+k]
			var temp float32
			if v > 0 {
				temp = v
			} else {
				temp = -v
			}
			if temp > p {
				p = temp
				k2 = i
			}
		}

		// A whole rest of 0 -> singular.
		if p == 0.0 {
			return false
		}

		// Permute rows k and k2 if needed.
		if k2 != k {
			permutations[k], permutations[k2] = permutations[k2], permutations[k]
			copy(pSwapArea, matrix[k2*nbCompo:k2*nbCompo+nbCompo])
			copy(matrix[k2*nbCompo:k2*nbCompo+nbCompo], matrix[k*nbCompo:k*nbCompo+nbCompo])
			copy(matrix[k*nbCompo:k*nbCompo+nbCompo], pSwapArea)
		}

		// Eliminate below the diagonal, storing multipliers in the lower part.
		temp := matrix[k*nbCompo+k]
		for i := k + 1; i < nbCompo; i++ {
			p = matrix[i*nbCompo+k] / temp
			matrix[i*nbCompo+k] = p
			for j := k + 1; j < nbCompo; j++ {
				matrix[i*nbCompo+j] -= p * matrix[k*nbCompo+j]
			}
		}
	}
	return true
}

// lupSolve is a port of opj_lupSolve. It solves matrix * result = vector where
// matrix is the LU decomposition and permutations the pivot order. intermediate
// is scratch of length nbCompo.
func lupSolve(result, matrix, vector []float32, permutations []uint32, nbCompo uint32, intermediate []float32) {
	// Forward substitution (unit lower-triangular L).
	for i := uint32(0); i < nbCompo; i++ {
		var sum float32
		for m := uint32(0); m < i; m++ {
			sum += matrix[i*nbCompo+m] * intermediate[m]
		}
		intermediate[i] = vector[permutations[i]] - sum
	}

	// Backward substitution (upper-triangular U).
	for k := int64(nbCompo) - 1; k != -1; k-- {
		uk := uint32(k)
		var sum float32
		u := matrix[uk*nbCompo+uk]
		for j := uk + 1; j < nbCompo; j++ {
			sum += matrix[uk*nbCompo+j] * result[j]
		}
		result[uk] = (intermediate[uk] - sum) / u
	}
}

// lupInvert is a port of opj_lupInvert. It inverts the LU-decomposed pSrcMatrix
// into pDestMatrix by solving against each unit basis vector.
func lupInvert(pSrcMatrix, pDestMatrix []float32, nbCompo uint32, permutations []uint32, pSrcTemp, pDestTemp, pSwapArea []float32) {
	for j := uint32(0); j < nbCompo; j++ {
		for i := range pSrcTemp {
			pSrcTemp[i] = 0
		}
		pSrcTemp[j] = 1.0
		lupSolve(pDestTemp, pSrcMatrix, pSrcTemp, permutations, nbCompo, pSwapArea)
		for i := uint32(0); i < nbCompo; i++ {
			pDestMatrix[i*nbCompo+j] = pDestTemp[i]
		}
	}
}
