// Package geometry classifies the values placing and sizing a popup. The
// vocabulary is tmux display-popup's — cells, percentages and tmux's
// single-letter position specifiers — and it is shared: the launch layer
// rejects a malformed value before it opens anything, and a backend whose
// multiplexer has no equivalent for a specifier has to recognize one to say so.
package geometry

import (
	"fmt"
	"strconv"
	"strings"
)

// positions are tmux's single-letter position specifiers. What each one means
// is documented where a caller meets them, on runinpopup.PopupSpec; here they
// are only a set to recognize, since they reach tmux untranslated.
const positions = "CRPMWS"

// IsPosition reports whether value is one of tmux's position specifiers.
func IsPosition(value string) bool {
	return len(value) == 1 && strings.Contains(positions, value)
}

// Validate reports whether value places or sizes a popup: empty (the backend's
// default), a count of cells, a percentage of the terminal, or — when position
// says the field takes one, which is true of X and Y only — a position
// specifier. field names the field in the error, and the value is quoted into
// it: the value is a user's typo far more often than it is a bug.
func Validate(field, value string, position bool) error {
	switch {
	case value == "", isCells(value), isPercent(value):
		return nil
	case IsPosition(value):
		if position {
			return nil
		}
		return fmt.Errorf(
			"popup geometry %s %q: a position specifier says where, not how big;"+
				" use cells or a percentage",
			field, value,
		)
	}
	return fmt.Errorf("popup geometry %s %q: want %s", field, value, syntax(position))
}

// Sum adds two values of one unit — cells to cells, a percentage of the
// terminal to a percentage of it — and reports whether there was a sum to give.
// Anything else has none: a cell count and a percentage cannot be added without
// the terminal size, which is the multiplexer's to know, and a position
// specifier is not a quantity at all. It is the caller that knows what the two
// values are and what to say when they do not add up.
func Sum(a, b string) (string, bool) {
	switch {
	case isCells(a) && isCells(b):
		return add(a, b, "")
	case isPercent(a) && isPercent(b):
		return add(strings.TrimSuffix(a, "%"), strings.TrimSuffix(b, "%"), "%")
	}
	return "", false
}

// add sums two digit strings and renders the total with unit appended. A count
// too large for an int has no sum either: the width of a terminal is what these
// measure, so a value that far out is a mistake rather than a total to compute.
func add(a, b, unit string) (string, bool) {
	x, errA := strconv.Atoi(a)
	y, errB := strconv.Atoi(b)
	if errA != nil || errB != nil {
		return "", false
	}
	return strconv.Itoa(x+y) + unit, true
}

// syntax spells out what a field accepts, for the error a malformed value gets.
func syntax(position bool) string {
	if position {
		return `cells, "N%" or one of the position specifiers C, R, P, M, W, S`
	}
	return `cells or "N%"`
}

// isCells reports a bare count of terminal cells. Digits only: a sign or the
// surrounding whitespace of a hand-edited value is a mistake worth reporting,
// not something to read through.
func isCells(value string) bool {
	return isDigits(value)
}

// isPercent reports a percentage of the terminal, "N%".
func isPercent(value string) bool {
	n, ok := strings.CutSuffix(value, "%")
	return ok && isDigits(n)
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
