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

package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/kyroy/kdtree"
)

func main() {
	embeddings := readEmbeddings("./tool/knn/embeddings.csv")
	query := readQuery("./tool/knn/query.csv")

	// convert embeddings to points
	points := make([]kdtree.Point, len(embeddings))
	for i, embedding := range embeddings {
		points[i] = &OpenAIEmbeddings{idx: i, data: embedding}
	}

	tree := kdtree.New(points)
	result := tree.KNN(&OpenAIEmbeddings{data: query}, 10)

	for _, item := range result {
		fmt.Printf("item: %v ", item.(*OpenAIEmbeddings).idx)
		similarity, err := dotProduct(query, item.(*OpenAIEmbeddings).data)
		if err != nil {
			panic(err)
		}
		fmt.Printf("similarity: %v\n", similarity)
	}
}

func dotProduct(v1, v2 []float64) (float64, error) {
	if len(v1) != len(v2) {
		return 0, fmt.Errorf("vectors must be the same length")
	}

	var result float64
	for i, val := range v1 {
		result += val * v2[i]
	}

	return result, nil
}

type OpenAIEmbeddings struct {
	data []float64
	idx  int
}

func (e *OpenAIEmbeddings) Dimensions() int {
	return len(e.data)
}

func (e *OpenAIEmbeddings) Dimension(i int) float64 {
	return e.data[i]
}

func readEmbeddings(path string) [][]float64 {
	embeddingsData, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}

	embeddings := make([][]float64, 0)
	for _, line := range strings.Split(string(embeddingsData), "\n") {
		if line == "" {
			continue
		}
		embeddings = append(embeddings, parseLine(line, ","))
	}

	return embeddings[1:] // skip header
}

func readQuery(path string) []float64 {
	queryData, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return parseLine(string(queryData), "\n")[1:] // skip header
}

func parseLine(line string, sep string) []float64 {
	result := make([]float64, 0)
	for _, value := range strings.Split(line, sep) {
		if value == "" {
			continue
		}
		result = append(result, parseValue(value))
	}
	return result
}

func parseValue(value string) float64 {
	val, err := strconv.ParseFloat(value, 64)
	if err != nil {
		panic(err)
	}
	return val
}
