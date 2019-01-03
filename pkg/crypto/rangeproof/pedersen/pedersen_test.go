package pedersen_test

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"gitlab.dusk.network/dusk-core/dusk-go/pkg/crypto/rangeproof/pedersen"
	"gitlab.dusk.network/dusk-core/dusk-go/pkg/crypto/ristretto"
)

func TestPedersenScalar(t *testing.T) {
	ped := pedersen.New([]byte("random data"))

	s := ristretto.Scalar{}
	s.Rand()

	commitment := ped.CommitToScalar(s)

	assert.NotEqual(t, nil, commitment)

}

func TestPedersenVector(t *testing.T) {
	ped := pedersen.New([]byte("some data"))
	var one ristretto.Scalar
	one.SetOne()

	var two ristretto.Scalar
	two.SetBigInt(big.NewInt(2))

	vec1 := []ristretto.Scalar{one, one}
	vec2 := []ristretto.Scalar{two, two}

	comm := ped.CommitToVectors(vec1, vec2)

	blind := comm.BlindingFactor

	H0 := ped.BlindPoint // blind
	H1 := ped.BasePoint
	H2 := ped.BaseVector.Bases[2]

	ped = pedersen.New(append(ped.GenData, uint8(1)))

	ped.BaseVector.Compute(2) // since values are not precomputed, we will compute two of them here

	B0 := ped.BlindPoint
	B1 := ped.BasePoint

	var H0blind ristretto.Point
	H0blind.ScalarMult(&H0, &blind)

	var H1one ristretto.Point
	H1one.ScalarMult(&H1, &one)

	var H2one ristretto.Point
	H2one.ScalarMult(&H2, &one)

	var B0two ristretto.Point
	B0two.ScalarMult(&B0, &two)

	var B1two ristretto.Point
	B1two.ScalarMult(&B1, &two)

	var expected ristretto.Point
	expected.Add(&H0blind, &H1one)
	expected.Add(&expected, &H2one)
	expected.Add(&expected, &B0two)
	expected.Add(&expected, &B1two)

	assert.Equal(t, expected.Bytes(), []byte(comm.Value.Bytes()))
}
