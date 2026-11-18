package persistence

import "strings"

type operationKindOrder struct {
	fallback int
	groups   [][]string
}

// The ordering of equal labels is delegated to the stable page tiebreaker.
func currentOperationKindSortExpr(locale string) (string, []any) {
	order, ok := currentOperationKindOrder[locale]
	if !ok {
		order = currentOperationKindOrder["en"]
	}
	var expression strings.Builder
	expression.WriteString("CASE op.kind")
	var args []any
	for rank, kinds := range order.groups {
		for _, kind := range kinds {
			expression.WriteString(" WHEN ? THEN ?")
			args = append(args, kind, rank)
		}
	}
	expression.WriteString(" ELSE ? END")
	return expression.String(), append(args, order.fallback)
}
