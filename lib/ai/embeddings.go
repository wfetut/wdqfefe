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
	"github.com/gravitational/trace"
	"github.com/kyroy/kdtree"
)

type EmbeddingsRetriever interface {
	GetRelevant(query *Embedding, k int) []*Embedding
	Insert(point *Embedding)
	Remove(name string) error
}

// Embedding is a vector embedding of a resource
type Embedding struct {
	// Vector is the embedding vector
	Vector []float64
	// Name is the name of the embedded resource, ex. node ID
	Name string
	// Data is the raw data of the embedded resource
	Data string
}

func (e *Embedding) Dimensions() int {
	return len(e.Vector)
}

func (e *Embedding) Dimension(i int) float64 {
	return e.Vector[i]
}

type KNNRetriever struct {
	tree    *kdtree.KDTree
	mapping map[string]*Embedding
}

func NewKNNRetriever(points []*Embedding) *KNNRetriever {
	kpoints := make([]kdtree.Point, len(points))
	mapping := make(map[string]*Embedding, len(points))
	for i, point := range points {
		kpoints[i] = point
		mapping[point.Name] = point
	}

	return &KNNRetriever{
		tree:    kdtree.New(kpoints),
		mapping: mapping,
	}
}

func (r *KNNRetriever) GetRelevant(query *Embedding, k int) []*Embedding {
	result := r.tree.KNN(query, k)
	relevant := make([]*Embedding, len(result))
	for i, item := range result {
		relevant[i] = item.(*Embedding)
	}
	return relevant
}

func (r *KNNRetriever) Insert(point *Embedding) {
	r.tree.Insert(point)
	r.mapping[point.Name] = point
}

func (r *KNNRetriever) Remove(name string) error {
	point, ok := r.mapping[name]
	if !ok {
		return trace.BadParameter("point %q not found", name)
	}

	delete(r.mapping, name)
	r.tree.Remove(kdtree.Point(point))

	return nil
}
