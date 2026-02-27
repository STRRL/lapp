// Copyright 2016-present Datadog, Inc.
// Licensed under the Apache License, Version 2.0
// Originally from github.com/DataDog/datadog-agent
// Modified for use in LAPP: removed Datadog-specific dependencies
// (config, metrics, status, message types), inlined token types.

package multiline

import (
	"math"
	"strings"
	"unsafe"
)

const maxRun = 10

var tokenLookup = makeTokenLookup()
var toUpperLookup = makeToUpperLookup()

func makeToUpperLookup() [256]byte {
	var lookup [256]byte
	for i := range lookup {
		lookup[i] = byte(i)
	}
	for c := byte('a'); c <= 'z'; c++ {
		lookup[c] = c - 32
	}
	return lookup
}

func makeTokenLookup() [256]Token {
	var lookup [256]Token

	for i := range lookup {
		lookup[i] = tC1
	}

	for c := byte('0'); c <= '9'; c++ {
		lookup[c] = tD1
	}

	lookup[' '] = tSpace
	lookup['\t'] = tSpace
	lookup['\n'] = tSpace
	lookup['\r'] = tSpace

	lookup[':'] = tColon
	lookup[';'] = tSemicolon
	lookup['-'] = tDash
	lookup['_'] = tUnderscore
	lookup['/'] = tFslash
	lookup['\\'] = tBslash
	lookup['.'] = tPeriod
	lookup[','] = tComma
	lookup['\''] = tSinglequote
	lookup['"'] = tDoublequote
	lookup['`'] = tBacktick
	lookup['~'] = tTilda
	lookup['*'] = tStar
	lookup['+'] = tPlus
	lookup['='] = tEqual
	lookup['('] = tParenopen
	lookup[')'] = tParenclose
	lookup['{'] = tBraceopen
	lookup['}'] = tBraceclose
	lookup['['] = tBracketopen
	lookup[']'] = tBracketclose
	lookup['&'] = tAmpersand
	lookup['!'] = tExclamation
	lookup['@'] = tAt
	lookup['#'] = tPound
	lookup['$'] = tDollar
	lookup['%'] = tPercent
	lookup['^'] = tUparrow

	return lookup
}

// tokenizer converts a log line prefix into a sequence of tokens.
type tokenizer struct {
	maxEvalBytes int
	strBuf       [maxRun]byte
	strLen       int
	tsBuf        []Token
	idxBuf       []int
}

func newTokenizer(maxEvalBytes int) *tokenizer {
	initCap := 64
	if maxEvalBytes > 0 && maxEvalBytes < initCap {
		initCap = maxEvalBytes
	}
	return &tokenizer{
		maxEvalBytes: maxEvalBytes,
		tsBuf:        make([]Token, 0, initCap),
		idxBuf:       make([]int, 0, initCap),
	}
}

func (t *tokenizer) emitToken(ts []Token, indicies []int, lastToken Token, run, idx int) ([]Token, []int) {
	if lastToken == tC1 && t.strLen > 0 && t.strLen <= 4 {
		if t.strLen == 1 {
			if specialToken := getSpecialShortToken(t.strBuf[0]); specialToken != tEnd {
				return append(ts, specialToken), append(indicies, idx)
			}
		} else {
			str := unsafe.String(&t.strBuf[0], t.strLen)
			if specialToken := getSpecialLongToken(str); specialToken != tEnd {
				return append(ts, specialToken), append(indicies, idx-run)
			}
		}
	}

	indicies = append(indicies, idx-run)
	if lastToken == tC1 || lastToken == tD1 {
		r := run
		if r >= maxRun {
			r = maxRun - 1
		}
		ts = append(ts, lastToken+Token(r))
	} else {
		ts = append(ts, lastToken)
	}
	return ts, indicies
}

func (t *tokenizer) tokenize(input []byte) ([]Token, []int) {
	inputLen := len(input)
	if inputLen == 0 {
		return nil, nil
	}

	estTokens := inputLen/4 + 8
	if cap(t.tsBuf) < estTokens {
		t.tsBuf = make([]Token, 0, estTokens)
		t.idxBuf = make([]int, 0, estTokens)
	}
	ts := t.tsBuf[:0]
	indicies := t.idxBuf[:0]

	run := 0
	firstChar := input[0]
	lastToken := tokenLookup[firstChar]

	t.strLen = 0
	if lastToken == tC1 {
		t.strBuf[0] = toUpperLookup[firstChar]
		t.strLen = 1
	}

	for i := 1; i < inputLen; i++ {
		char := input[i]
		currentToken := tokenLookup[char]

		if currentToken != lastToken {
			ts, indicies = t.emitToken(ts, indicies, lastToken, run, i-1)
			run = 0
			t.strLen = 0
		} else {
			run++
		}

		if currentToken == tC1 && t.strLen < maxRun {
			t.strBuf[t.strLen] = toUpperLookup[char]
			t.strLen++
		}

		lastToken = currentToken
	}

	ts, indicies = t.emitToken(ts, indicies, lastToken, run, inputLen-1)

	t.tsBuf = ts
	t.idxBuf = indicies

	n := len(ts)
	result := make([]Token, n)
	copy(result, ts)
	resultIdx := make([]int, n)
	copy(resultIdx, indicies)
	return result, resultIdx
}

func getSpecialShortToken(char byte) Token {
	if char == 'T' {
		return tT
	}
	if char == 'Z' {
		return tZone
	}
	return tEnd
}

func getSpecialLongToken(input string) Token {
	switch len(input) {
	case 2:
		if input == "AM" || input == "PM" {
			return tApm
		}
	case 3:
		switch input {
		case "JAN", "FEB", "MAR", "APR", "MAY", "JUN",
			"JUL", "AUG", "SEP", "OCT", "NOV", "DEC":
			return tMonth
		case "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN":
			return tDay
		case "UTC", "GMT", "EST", "EDT", "CST", "CDT",
			"MST", "MDT", "PST", "PDT", "JST", "KST",
			"IST", "MSK", "CET", "BST", "HST", "HDT",
			"NST", "NDT":
			return tZone
		}
	case 4:
		switch input {
		case "CEST", "NZST", "NZDT", "ACST", "ACDT",
			"AEST", "AEDT", "AWST", "AWDT", "AKST",
			"AKDT", "CHST", "CHDT":
			return tZone
		}
	}
	return tEnd
}

// tokenToString converts a single token to a debug string.
func tokenToString(token Token) string {
	if token >= tD1 && token <= tD10 {
		return strings.Repeat("D", int(token-tD1)+1)
	} else if token >= tC1 && token <= tC10 {
		return strings.Repeat("C", int(token-tC1)+1)
	}

	switch token {
	case tSpace:
		return " "
	case tColon:
		return ":"
	case tSemicolon:
		return ";"
	case tDash:
		return "-"
	case tUnderscore:
		return "_"
	case tFslash:
		return "/"
	case tBslash:
		return "\\"
	case tPeriod:
		return "."
	case tComma:
		return ","
	case tSinglequote:
		return "'"
	case tDoublequote:
		return "\""
	case tBacktick:
		return "`"
	case tTilda:
		return "~"
	case tStar:
		return "*"
	case tPlus:
		return "+"
	case tEqual:
		return "="
	case tParenopen:
		return "("
	case tParenclose:
		return ")"
	case tBraceopen:
		return "{"
	case tBraceclose:
		return "}"
	case tBracketopen:
		return "["
	case tBracketclose:
		return "]"
	case tAmpersand:
		return "&"
	case tExclamation:
		return "!"
	case tAt:
		return "@"
	case tPound:
		return "#"
	case tDollar:
		return "$"
	case tPercent:
		return "%"
	case tUparrow:
		return "^"
	case tMonth:
		return "MTH"
	case tDay:
		return "DAY"
	case tApm:
		return "PM"
	case tT:
		return "T"
	case tZone:
		return "ZONE"
	}
	return ""
}

func tokensToString(tokens []Token) string {
	var builder strings.Builder
	for _, t := range tokens {
		builder.WriteString(tokenToString(t))
	}
	return builder.String()
}

func isMatch(seqA []Token, seqB []Token, thresh float64) bool {
	count := min(len(seqB), len(seqA))

	if count == 0 {
		return len(seqA) == len(seqB)
	}

	requiredMatches := int(math.Round(thresh * float64(count)))
	match := 0

	for i := 0; i < count; i++ {
		if seqA[i] == seqB[i] {
			match++
		}
		if match+(count-i-1) < requiredMatches {
			return false
		}
	}

	return true
}
