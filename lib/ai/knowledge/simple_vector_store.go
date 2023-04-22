/*
Copyright 2023 Gravitational, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package ai

import (
	"math"
	"sort"
)

type SimpleVectorStore struct {
	embeddingMap map[string][]float64
	nodeIdDocId  map[string]string
	nodes        map[string]*Node
}

func NewSimpleVectorStore() *SimpleVectorStore {
	return &SimpleVectorStore{
		embeddingMap: make(map[string][]float64),
		nodeIdDocId:  make(map[string]string),
		nodes:        make(map[string]*Node),
	}
}

func (vi *SimpleVectorStore) Add(nodes []*EmbeddedNode) error {
	for _, node := range nodes {
		vi.embeddingMap[node.node.id] = node.embedding
		vi.nodeIdDocId[node.node.id] = node.node.docId
	}

	return nil
}

func (vi *SimpleVectorStore) Query(embedding []float64, similarityTopK int) []*Node {
	ids := getTopKEmbeddings(vi.embeddingMap, embedding, similarityTopK)
	nodes := make([]*Node, 0, len(ids))
	for _, id := range ids {
		nodes = append(nodes, vi.nodes[id])
	}

	return nodes
}

func similarity(embedding1 []float64, embedding2 []float64) float64 {
	var product float64
	for i := 0; i < len(embedding1); i++ {
		product += embedding1[i] * embedding2[i]
	}

	var norm1 float64
	var norm2 float64
	for i := 0; i < len(embedding1); i++ {
		norm1 += embedding1[i] * embedding1[i]
		norm2 += embedding2[i] * embedding2[i]
	}

	norm := math.Sqrt(norm1) * math.Sqrt(norm2)
	return product / norm
}

func getTopKEmbeddings(embeddings map[string][]float64, queryEmbedding []float64, similarityTopK int) []string {
	similarities := make([]struct {
		id         string
		similarity float64
	}, 0, len(embeddings))

	for id, emb := range embeddings {
		entry := struct {
			id         string
			similarity float64
		}{
			id:         id,
			similarity: similarity(emb, queryEmbedding),
		}

		similarities = append(similarities, entry)
	}

	sort.Slice(similarities, func(i, j int) bool {
		return similarities[i].similarity < similarities[j].similarity
	})

	ids := make([]string, 0, similarityTopK)
	for i := 0; i < similarityTopK; i++ {
		ids = append(ids, similarities[i].id)
	}

	return ids
}
