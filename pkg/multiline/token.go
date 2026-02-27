// Copyright 2016-present Datadog, Inc.
// Licensed under the Apache License, Version 2.0
// Originally from github.com/DataDog/datadog-agent
// Modified for use in LAPP: removed Datadog-specific dependencies
// (config, metrics, status, message types).

package multiline

// Token is the type that represents a single token.
type Token byte

const (
	tSpace Token = iota

	// Special Characters
	tColon        // :
	tSemicolon    // ;
	tDash         // -
	tUnderscore   // _
	tFslash       // /
	tBslash       // \
	tPeriod       // .
	tComma        // ,
	tSinglequote  // '
	tDoublequote  // "
	tBacktick     // `
	tTilda        // ~
	tStar         // *
	tPlus         // +
	tEqual        // =
	tParenopen    // (
	tParenclose   // )
	tBraceopen    // {
	tBraceclose   // }
	tBracketopen  // [
	tBracketclose // ]
	tAmpersand    // &
	tExclamation  // !
	tAt           // @
	tPound        // #
	tDollar       // $
	tPercent      // %
	tUparrow      // ^

	// Digit runs
	tD1
	tD2
	tD3
	tD4
	tD5
	tD6
	tD7
	tD8
	tD9
	tD10

	// Char runs
	tC1
	tC2
	tC3
	tC4
	tC5
	tC6
	tC7
	tC8
	tC9
	tC10

	// Special tokens
	tMonth
	tDay
	tApm  // am or pm
	tZone // timezone
	tT    // t (often `T`) denotes a time separator
	tEnd  // marks end of token list
)
