package gomutant

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

const attributionSeparator = " — "

var attributionOps = []string{"open", "stat", "chdir"}

const movedBracketClausePrefix = "observation bracket moved"

var bracketAttributionLabels = []string{"added: ", "removed: ", "recently touched: "}

func refusalClause(reason string) string {
	clause, _ := splitAttribution(reason)
	return clause
}

func refusalAttribution(reason string) string {
	_, attribution := splitAttribution(reason)
	return attribution
}

func splitAttribution(reason string) (clause, attribution string) {
	if clause, attribution, ok := splitBracketAttribution(reason); ok {
		return clause, attribution
	}
	for i := strings.Index(reason, attributionSeparator); i >= 0; {
		if suffix := reason[i+len(attributionSeparator):]; attributionWellFormed(suffix) {
			return reason[:i], suffix
		}
		next := strings.Index(reason[i+1:], attributionSeparator)
		if next < 0 {
			break
		}
		i += 1 + next
	}
	return reason, ""
}

func attributionWellFormed(s string) bool {
	if _, _, _, ok := parseOperationAttribution(s); ok {
		return true
	}
	if rest, ok := strings.CutPrefix(s, "recorded path "); ok {
		return quotedThen(rest, " resolves to ", " outside the tree")
	}
	return false
}

func parseOperationAttribution(s string) (op, quotedName, dir string, ok bool) {
	for _, candidate := range attributionOps {
		rest, found := strings.CutPrefix(s, candidate+" ")
		if !found {
			continue
		}
		name, rest, found := quotedPrefixThen(rest, " in ")
		if !found {
			return "", "", "", false
		}
		quoted, tail, found := quotedPrefixThen(rest, "")
		if !found || tail != "" {
			return "", "", "", false
		}
		unquoted, err := strconv.Unquote(quoted)
		if err != nil {
			return "", "", "", false
		}
		return candidate, name, unquoted, true
	}
	return "", "", "", false
}

func bracketAttributionWellFormed(s string) bool {
	inner, ok := strings.CutPrefix(s, "[")
	if !ok {
		return false
	}
	inner, ok = strings.CutSuffix(inner, "]")
	if !ok {
		return false
	}
	for _, part := range bracketAttributionParts(inner) {
		labelled := false
		for _, label := range bracketAttributionLabels {
			if strings.HasPrefix(part, label) {
				labelled = true
			}
		}
		if !labelled {
			return false
		}
	}
	return true
}

func bracketAttributionParts(inner string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(inner); {
		switch {
		case inner[i] == '"':
			quoted, err := strconv.QuotedPrefix(inner[i:])
			if err != nil {
				i++
				continue
			}
			i += len(quoted)
		case strings.HasPrefix(inner[i:], "; "):
			parts = append(parts, inner[start:i])
			i += 2
			start = i
		default:
			i++
		}
	}
	return append(parts, inner[start:])
}

func splitBracketAttribution(reason string) (clause, attribution string, ok bool) {
	if !strings.HasPrefix(reason, movedBracketClausePrefix) {
		return reason, "", false
	}
	if rest, found := strings.CutPrefix(reason, movedBracketClausePrefix+": \""); found {
		if root, list, quoted := quotedPrefixThen("\""+rest, " "); quoted {
			if bracketAttributionWellFormed(list) {
				return movedBracketClausePrefix + ": " + root, list[1 : len(list)-1], true
			}
			return reason, "", false
		} else if _, _, quotedRootOnly := quotedPrefixThen("\""+rest, ""); quotedRootOnly {
			return reason, "", false
		}
	}
	for i := strings.Index(reason, " ["); i >= 0; {
		if suffix := reason[i+1:]; bracketAttributionWellFormed(suffix) {
			return reason[:i], suffix[1 : len(suffix)-1], true
		}
		next := strings.Index(reason[i+1:], " [")
		if next < 0 {
			break
		}
		i += 1 + next
	}
	return reason, "", false
}

func quotedPrefixThen(s, prefix string) (quoted, rest string, ok bool) {
	q, err := strconv.QuotedPrefix(s)
	if err != nil {
		return "", "", false
	}
	rest, ok = strings.CutPrefix(s[len(q):], prefix)
	if !ok {
		return "", "", false
	}
	return q, rest, true
}

func quotedThen(s, mid, tail string) bool {
	_, rest, ok := quotedPrefixThen(s, mid)
	if !ok {
		return false
	}
	_, after, ok := quotedPrefixThen(rest, tail)
	return ok && after == ""
}

func movedBracketClause(root string) string {
	return movedBracketClausePrefix + ": " + memberListName(root)
}

func canonicalMovedBracketClause(clause string) string {
	root, found := strings.CutPrefix(clause, movedBracketClausePrefix+": ")
	if !found {
		return clause
	}
	if name, err := strconv.Unquote(root); err == nil && memberListName(name) == root {
		return clause
	}
	return movedBracketClause(root)
}

func memberListName(name string) string {
	if !utf8.ValidString(name) || strings.ContainsAny(name, "\x00\r\n[]\"") || strings.Contains(name, "; ") {
		return strconv.Quote(name)
	}
	return name
}
