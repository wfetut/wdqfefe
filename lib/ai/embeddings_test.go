/*
 * Copyright 2023 Gravitational, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package ai

import (
	"math/rand"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKNNRetriever_GetRelevant(t *testing.T) {
	randGen := rand.New(rand.NewSource(42))

	generateVector := func() []float64 {
		// generate random vector
		// reduce the dimensionality to 100
		vec := make([]float64, 100)
		for i := 0; i < 100; i++ {
			vec[i] = randGen.Float64()
		}
		return vec
	}

	points := make([]*Embedding, 100)
	for i := 0; i < 100; i++ {
		points[i] = &Embedding{
			Vector: generateVector(),
			Name:   strconv.Itoa(i),
			Data:   strconv.Itoa(i),
		}
	}

	query := &Embedding{
		Vector: generateVector(),
	}

	retriever := NewKNNRetriever(points)
	docs := retriever.GetRelevant(query, 10)

	require.Len(t, docs, 10)
	require.Equal(t, "92", docs[0].Name)
	require.Equal(t, "57", docs[1].Name)
	require.Equal(t, "56", docs[2].Name)
	require.Equal(t, "95", docs[3].Name)
	require.Equal(t, "49", docs[4].Name)
	require.Equal(t, "47", docs[5].Name)
	require.Equal(t, "30", docs[6].Name)
	require.Equal(t, "99", docs[7].Name)
	require.Equal(t, "12", docs[8].Name)
	require.Equal(t, "96", docs[9].Name)
}

func TestKNNRetriever_Insert(t *testing.T) {
	points := []*Embedding{
		{
			Vector: []float64{1, 2, 3},
			Name:   "1",
			Data:   "1",
		},
		{
			Vector: []float64{4, 5, 6},
			Name:   "2",
			Data:   "2",
		},
	}

	retriever := NewKNNRetriever(points)
	docs1 := retriever.GetRelevant(&Embedding{
		Vector: []float64{7, 8, 9},
	}, 10)
	require.Len(t, docs1, 2)

	retriever.Insert(&Embedding{
		Vector: []float64{7, 8, 9},
		Name:   "3",
		Data:   "3",
	})
	docs2 := retriever.GetRelevant(&Embedding{
		Vector: []float64{7, 8, 9},
	}, 10)
	require.Len(t, docs2, 3)
}

func TestKNNRetriever_Remove(t *testing.T) {
	points := []*Embedding{
		{
			Vector: []float64{1, 2, 3},
			Name:   "1",
			Data:   "1",
		},
		{
			Vector: []float64{4, 5, 6},
			Name:   "2",
			Data:   "2",
		},
		{
			Vector: []float64{7, 8, 9},
			Name:   "3",
			Data:   "3",
		},
	}

	retriever := NewKNNRetriever(points)
	docs1 := retriever.GetRelevant(&Embedding{
		Vector: []float64{7, 8, 9},
	}, 10)

	require.Len(t, docs1, 3)

	err := retriever.Remove("2")
	require.NoError(t, err)

	docs2 := retriever.GetRelevant(&Embedding{
		Vector: []float64{7, 8, 9},
	}, 10)
	require.Len(t, docs2, 2)
}
