package query

import (
	"fmt"
	"regexp"

	"github.com/sourcegraph/sourcegraph/internal/search/query/syntax"
	"github.com/sourcegraph/sourcegraph/internal/search/query/types"
)

type SearchType int

const (
	SearchTypeRegex SearchType = iota
	SearchTypeLiteral
	SearchTypeStructural
)

// QueryInfo is an intermediate type for an interface of both ordinary queries
// and and/or query processing. The and/or query processing will become the
// canonical query form and the QueryInfo type will be removed.
type QueryInfo interface {
	RegexpPatterns(field string) (values, negatedValues []string)
	StringValues(field string) (values, negatedValues []string)
	StringValue(field string) (value, negatedValue string)
	Values(field string) []*types.Value
	Fields() map[string][]*types.Value
	BoolValue(field string) bool
	IsCaseSensitive() bool
	ParseTree() syntax.ParseTree
}

// An ordinary query (not containing and/or expressions).
type OrdinaryQuery struct {
	Query     *Query           // the validated search query
	parseTree syntax.ParseTree // the parsed search query
}

// A query containing and/or expressions.
type AndOrQuery struct {
	Query []Node
}

func (q OrdinaryQuery) RegexpPatterns(field string) (values, negatedValues []string) {
	return q.Query.RegexpPatterns(field)
}
func (q OrdinaryQuery) StringValues(field string) (values, negatedValues []string) {
	return q.Query.StringValues(field)
}
func (q OrdinaryQuery) StringValue(field string) (value, negatedValue string) {
	return q.Query.StringValue(field)
}
func (q OrdinaryQuery) Values(field string) []*types.Value {
	return q.Query.Values(field)
}
func (q OrdinaryQuery) Fields() map[string][]*types.Value {
	return q.Query.Fields
}
func (q OrdinaryQuery) ParseTree() syntax.ParseTree {
	return q.parseTree
}
func (q OrdinaryQuery) BoolValue(field string) bool {
	return q.Query.BoolValue(field)
}
func (q OrdinaryQuery) IsCaseSensitive() bool {
	return q.Query.IsCaseSensitive()
}

// AndOrQuery satisfies the interface for QueryInfo close to that of OrdinaryQuery.
func (q AndOrQuery) RegexpPatterns(field string) (values, negatedValues []string) {
	VisitField(q.Query, field, func(visitedValue string, negated bool) {
		if negated {
			negatedValues = append(negatedValues, visitedValue)
		} else {
			values = append(values, visitedValue)
		}
	})
	return values, negatedValues
}

func (q AndOrQuery) StringValues(field string) (values, negatedValues []string) {
	VisitField(q.Query, field, func(visitedValue string, negated bool) {
		if negated {
			negatedValues = append(negatedValues, visitedValue)
		} else {
			values = append(values, visitedValue)
		}
	})
	return values, negatedValues
}

func (q AndOrQuery) StringValue(field string) (value, negatedValue string) {
	VisitField(q.Query, field, func(visitedValue string, negated bool) {
		if negated {
			negatedValue = visitedValue
		} else {
			value = visitedValue
		}
	})
	return value, negatedValue
}

func (q AndOrQuery) Values(field string) []*types.Value {
	var values []*types.Value
	if field == "" {
		VisitPattern(q.Query, func(value string, _, quoted bool) {
			values = append(values, valueToTypedValue(field, value, quoted)...)
		})
	} else {
		VisitField(q.Query, field, func(value string, _ bool) {
			values = append(values, valueToTypedValue(field, value, false)...)
		})
	}
	return values
}

func (q AndOrQuery) Fields() map[string][]*types.Value {
	fields := make(map[string][]*types.Value)
	VisitPattern(q.Query, func(value string, _, quoted bool) {
		fields[""] = q.Values("")
	})
	VisitParameter(q.Query, func(field, _ string, _ bool) {
		fields[field] = q.Values(field)
	})
	return fields
}

// ParseTree returns a flat, mock-like parse tree of an and/or query. The parse
// tree values are currently only significant in alerts. Whether it is empty or
// not is significant for surfacing suggestions.
func (q AndOrQuery) ParseTree() syntax.ParseTree {
	var tree syntax.ParseTree
	VisitPattern(q.Query, func(value string, negated, _ bool) {
		expr := &syntax.Expr{
			Field: "",
			Value: value,
			Not:   negated,
		}
		tree = append(tree, expr)
	})
	VisitParameter(q.Query, func(field, value string, negated bool) {
		expr := &syntax.Expr{
			Field: field,
			Value: value,
			Not:   negated,
		}
		tree = append(tree, expr)
	})
	return tree
}

func (q AndOrQuery) BoolValue(field string) bool {
	result := false
	VisitField(q.Query, field, func(value string, _ bool) {
		result, _ = parseBool(value) // err was checked during parsing and validation.
	})
	return result
}

func (q AndOrQuery) IsCaseSensitive() bool {
	return q.BoolValue("case")
}

func parseRegexpOrPanic(field, value string) *regexp.Regexp {
	regexp, err := regexp.Compile(value)
	if err != nil {
		panic(fmt.Sprintf("Value %s for field %s invalid regex: %s", field, value, err.Error()))
	}
	return regexp
}

// valueToTypedValue approximately preserves the field validation for
// OrdinaryQuery processing. It does not check the validity of field negation or
// if the same field is specified more than once.
func valueToTypedValue(field, value string, quoted bool) []*types.Value {
	switch field {
	case
		FieldDefault:
		if quoted {
			return []*types.Value{{String: &value}}
		}
		if regexp, err := regexp.Compile(value); err == nil {
			return []*types.Value{{Regexp: regexp}}
		}
		return []*types.Value{{String: &value}}

	case
		FieldCase:
		b, _ := parseBool(value)
		return []*types.Value{{Bool: &b}}

	case
		FieldRepo, "r":
		return []*types.Value{{Regexp: parseRegexpOrPanic(field, value)}}

	case
		FieldRepoGroup, "g":
		return []*types.Value{{String: &value}}

	case
		FieldFile, "f":
		return []*types.Value{{Regexp: parseRegexpOrPanic(field, value)}}

	case
		FieldFork,
		FieldArchived,
		FieldLang, "l", "language",
		FieldType,
		FieldPatternType,
		FieldContent:
		return []*types.Value{{String: &value}}

	case FieldRepoHasFile:
		return []*types.Value{{Regexp: parseRegexpOrPanic(field, value)}}

	case
		FieldRepoHasCommitAfter,
		FieldBefore, "until",
		FieldAfter, "since":
		return []*types.Value{{String: &value}}

	case
		FieldAuthor,
		FieldCommitter,
		FieldMessage, "m", "msg":
		return []*types.Value{{Regexp: parseRegexpOrPanic(field, value)}}

	case
		FieldIndex,
		FieldCount,
		FieldMax,
		FieldTimeout,
		FieldReplace,
		FieldCombyRule:
		return []*types.Value{{String: &value}}
	}
	return []*types.Value{{String: &value}}
}
