// Copyright 2016-present Datadog, Inc.
// Licensed under the Apache License, Version 2.0
// Originally from github.com/DataDog/datadog-agent
// Modified for use in LAPP: removed Datadog-specific dependencies
// (config, metrics, status, message types), inlined token types.

package multiline

// tokenGraph is a directed cyclic graph of tokens that models the relationship
// between any two tokens. It is used to calculate the probability of an unknown
// sequence of tokens being a known timestamp format.
type tokenGraph struct {
	adjacencies        [][]bool
	minimumTokenLength int
}

// matchContext is the context of a match.
type matchContext struct {
	probability float64
	start       int
	end         int
}

func newTokenGraph(minimumTokenLength int, inputData [][]Token) *tokenGraph {
	g := &tokenGraph{
		adjacencies:        make([][]bool, tEnd),
		minimumTokenLength: minimumTokenLength,
	}
	for _, tokens := range inputData {
		g.add(tokens)
	}
	return g
}

func (m *tokenGraph) add(ts []Token) {
	lastToken := ts[0]
	for _, token := range ts[1:] {
		if m.adjacencies[lastToken] == nil {
			m.adjacencies[lastToken] = make([]bool, tEnd)
		}
		m.adjacencies[lastToken][token] = true
		lastToken = token
	}
}

// matchProbability returns the probability of a sequence of tokens being
// represented by the graph.
func (m *tokenGraph) matchProbability(ts []Token) matchContext {
	if len(ts) < m.minimumTokenLength {
		return matchContext{}
	}

	lastToken := ts[0]
	matchForIndex := func(idx int) int {
		match := -1
		if len(m.adjacencies[lastToken]) > 0 && m.adjacencies[lastToken][ts[idx+1]] {
			match = 1
		}
		lastToken = ts[idx+1]
		return match
	}

	avg, start, end := maxSubsequence(len(ts)-1, matchForIndex)

	if end-start < m.minimumTokenLength {
		return matchContext{}
	}

	return matchContext{
		probability: avg,
		start:       start,
		end:         end,
	}
}

// maxSubsequence is a modified Kadane's Algorithm that returns the average,
// start, and end of the largest subsequence.
//
//nolint:gocritic // unnamedResult: vendored from Datadog agent, keeping original signature
func maxSubsequence(length int, matchForIndex func(idx int) int) (float64, int, int) {
	if length == 0 {
		return 0, 0, 0
	}
	maxSum := matchForIndex(0)
	currentSum := maxSum
	start := 0
	end := 0
	tempStart := 0

	for i := 1; i < length; i++ {
		v := matchForIndex(i)
		if v > currentSum+v {
			currentSum = v
			tempStart = i
		} else {
			currentSum += v
		}

		if currentSum > maxSum {
			maxSum = currentSum
			start = tempStart
			end = i
		}
	}
	end++
	return float64(maxSum) / float64(end-start), start, end
}
