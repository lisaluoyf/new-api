package service

import (
	"math"
	"strings"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
)

// flatTieredPricing converts a single linear tier expression into the legacy
// channel price tuple. ChannelModelPricing can represent one price per token
// class, but cannot represent conditions, request rules, or multiple tiers.
// Unsupported expressions deliberately return false so the caller keeps the
// existing ratio-based fallback instead of silently inventing a price.
func flatTieredPricing(mode, expr string) (ModelUnitPricesUSD, bool) {
	if strings.TrimSpace(mode) != "tiered_expr" || strings.TrimSpace(expr) == "" || strings.Contains(expr, "|||") {
		return ModelUnitPricesUSD{}, false
	}
	_, body := billingexpr.ParseExprVersion(strings.TrimSpace(expr))
	tree, err := parser.Parse(body)
	if err != nil {
		return ModelUnitPricesUSD{}, false
	}
	call, ok := tree.Node.(*ast.CallNode)
	if !ok || len(call.Arguments) != 2 {
		return ModelUnitPricesUSD{}, false
	}
	callee, ok := call.Callee.(*ast.IdentifierNode)
	if !ok || callee.Value != "tier" {
		return ModelUnitPricesUSD{}, false
	}
	if _, ok := call.Arguments[0].(*ast.StringNode); !ok {
		return ModelUnitPricesUSD{}, false
	}
	linear, ok := parseLinearTokenPrice(call.Arguments[1])
	if !ok {
		return ModelUnitPricesUSD{}, false
	}
	if math.Abs(linear.constant) > 0.0000001 || linear.coefficients["p"] <= 0 || linear.coefficients["c"] <= 0 {
		return ModelUnitPricesUSD{}, false
	}
	for _, coefficient := range linear.coefficients {
		if coefficient < 0 || math.IsNaN(coefficient) || math.IsInf(coefficient, 0) {
			return ModelUnitPricesUSD{}, false
		}
	}

	cache := linear.coefficients["cr"]
	if cache == 0 {
		cache = linear.coefficients["p"]
	}
	cacheCreation := linear.coefficients["cc"]
	if cacheCreation == 0 {
		cacheCreation = linear.coefficients["p"]
	}
	return ModelUnitPricesUSD{
		InputPrice:         linear.coefficients["p"],
		OutputPrice:        linear.coefficients["c"],
		CachePrice:         cache,
		CacheCreationPrice: cacheCreation,
	}, true
}

type linearTokenPrice struct {
	constant     float64
	coefficients map[string]float64
}

func parseLinearTokenPrice(node ast.Node) (linearTokenPrice, bool) {
	scalar := func(value float64) linearTokenPrice {
		return linearTokenPrice{constant: value, coefficients: map[string]float64{}}
	}
	switch n := node.(type) {
	case *ast.IntegerNode:
		return scalar(float64(n.Value)), true
	case *ast.FloatNode:
		return scalar(n.Value), true
	case *ast.IdentifierNode:
		switch n.Value {
		case "p", "c", "cr", "cc":
			return linearTokenPrice{coefficients: map[string]float64{n.Value: 1}}, true
		default:
			return linearTokenPrice{}, false
		}
	case *ast.UnaryNode:
		value, ok := parseLinearTokenPrice(n.Node)
		if !ok || (n.Operator != "+" && n.Operator != "-") {
			return linearTokenPrice{}, false
		}
		if n.Operator == "-" {
			value = scaleLinearTokenPrice(value, -1)
		}
		return value, true
	case *ast.BinaryNode:
		left, leftOK := parseLinearTokenPrice(n.Left)
		right, rightOK := parseLinearTokenPrice(n.Right)
		if !leftOK || !rightOK {
			return linearTokenPrice{}, false
		}
		switch n.Operator {
		case "+":
			return addLinearTokenPrices(left, right, 1), true
		case "-":
			return addLinearTokenPrices(left, right, -1), true
		case "*":
			if len(left.coefficients) == 0 {
				return scaleLinearTokenPrice(right, left.constant), true
			}
			if len(right.coefficients) == 0 {
				return scaleLinearTokenPrice(left, right.constant), true
			}
		case "/":
			if len(right.coefficients) == 0 && right.constant != 0 {
				return scaleLinearTokenPrice(left, 1/right.constant), true
			}
		}
	}
	return linearTokenPrice{}, false
}

func scaleLinearTokenPrice(price linearTokenPrice, multiplier float64) linearTokenPrice {
	result := linearTokenPrice{constant: price.constant * multiplier, coefficients: make(map[string]float64, len(price.coefficients))}
	for name, coefficient := range price.coefficients {
		result.coefficients[name] = coefficient * multiplier
	}
	return result
}

func addLinearTokenPrices(left, right linearTokenPrice, rightMultiplier float64) linearTokenPrice {
	result := linearTokenPrice{constant: left.constant + right.constant*rightMultiplier, coefficients: make(map[string]float64, len(left.coefficients)+len(right.coefficients))}
	for name, coefficient := range left.coefficients {
		result.coefficients[name] = coefficient
	}
	for name, coefficient := range right.coefficients {
		result.coefficients[name] += coefficient * rightMultiplier
	}
	return result
}
