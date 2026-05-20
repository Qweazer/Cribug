package activities

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"

	"cribug/internal/types"
)

const (
	maxExprLength   = 256
	maxNestingDepth = 16
)

// ToolActivities handles safe tool execution
type ToolActivities struct{}

func NewToolActivities() *ToolActivities {
	return &ToolActivities{}
}

// ExecuteTool executes a tool by name with given arguments and emits events
func (a *ToolActivities) ExecuteTool(ctx context.Context, input ToolExecuteInput) (*types.ToolResult, error) {
	start := time.Now()

	var result *types.ToolResult
	var err error

	switch input.ToolName {
	case "calculator":
		result, err = a.executeCalculator(input.Arguments, start)
	case "echo":
		result, err = a.executeEcho(input.Arguments, start)
	default:
		result = &types.ToolResult{
			ToolName:  input.ToolName,
			Error:     "unknown tool: " + input.ToolName,
			LatencyMs: int(time.Since(start).Milliseconds()),
		}
	}

	return result, err
}

type ToolExecuteInput struct {
	TaskID     string
	ToolName   string
	Arguments  map[string]interface{}
}

func (a *ToolActivities) executeCalculator(arguments map[string]interface{}, start time.Time) (*types.ToolResult, error) {
	expr, ok := arguments["expr"].(string)
	if !ok {
		return &types.ToolResult{
			ToolName:  "calculator",
			Error:     "invalid arguments: 'expr' string required",
			LatencyMs: int(time.Since(start).Milliseconds()),
		}, nil
	}

	// Validate expression length
	if len(expr) > maxExprLength {
		return &types.ToolResult{
			ToolName:  "calculator",
			Error:     "expression too long: max 256 characters",
			LatencyMs: int(time.Since(start).Milliseconds()),
		}, nil
	}

	// Evaluate using safe parser
	result, evalErr := evaluateArithmetic(expr)
	if evalErr != nil {
		return &types.ToolResult{
			ToolName:  "calculator",
			Error:     evalErr.Error(),
			LatencyMs: int(time.Since(start).Milliseconds()),
		}, nil
	}

	return &types.ToolResult{
		ToolName:  "calculator",
		Output:    result,
		LatencyMs: int(time.Since(start).Milliseconds()),
	}, nil
}

func (a *ToolActivities) executeEcho(arguments map[string]interface{}, start time.Time) (*types.ToolResult, error) {
	msg, ok := arguments["message"].(string)
	if !ok {
		return &types.ToolResult{
			ToolName:  "echo",
			Error:     "invalid arguments: 'message' string required",
			LatencyMs: int(time.Since(start).Milliseconds()),
		}, nil
	}

	return &types.ToolResult{
		ToolName:  "echo",
		Output:    msg,
		LatencyMs: int(time.Since(start).Milliseconds()),
	}, nil
}

// Safe arithmetic evaluator using token-based parsing (no eval, no os/exec)
// Only allows: digits, whitespace, +, -, *, /, (, ), .
func evaluateArithmetic(expr string) (string, error) {
	// Tokenize and validate
	tokens, err := tokenize(expr)
	if err != nil {
		return "", errors.New("invalid expression: " + err.Error())
	}

	if len(tokens) == 0 {
		return "", errors.New("empty expression")
	}

	// Parse and evaluate with depth tracking
	result, _, err := parseExpression(tokens, 0, 0)
	if err != nil {
		return "", err
	}

	return formatResult(result), nil
}

type token struct {
	kind  tokenKind
	value string
}

type tokenKind int

const (
	tokenNumber tokenKind = iota
	tokenPlus
	tokenMinus
	tokenMultiply
	tokenDivide
	tokenLeftParen
	tokenRightParen
	tokenEOF
)

func (t tokenKind) String() string {
	switch t {
	case tokenNumber:
		return "NUMBER"
	case tokenPlus:
		return "PLUS"
	case tokenMinus:
		return "MINUS"
	case tokenMultiply:
		return "MULTIPLY"
	case tokenDivide:
		return "DIVIDE"
	case tokenLeftParen:
		return "LPAREN"
	case tokenRightParen:
		return "RPAREN"
	case tokenEOF:
		return "EOF"
	default:
		return "UNKNOWN"
	}
}

// tokenize scans the expression and returns tokens
// Only accepts digits, whitespace, +, -, *, /, (, ), .
func tokenize(expr string) ([]token, error) {
	var tokens []token
	i := 0

	// Skip leading unary minus by prepending 0
	hasLeadingMinus := false
	trimmed := strings.TrimLeft(expr, " \t")
	if strings.HasPrefix(trimmed, "-") {
		hasLeadingMinus = true
		expr = "0" + expr
	}

	for i < len(expr) {
		ch := rune(expr[i])

		// Skip whitespace
		if unicode.IsSpace(ch) {
			i++
			continue
		}

		// Numbers (including decimals)
		if unicode.IsDigit(ch) || (ch == '.' && i+1 < len(expr) && unicode.IsDigit(rune(expr[i+1]))) {
			start := i
			hasDecimal := false
			for i < len(expr) {
				ch := rune(expr[i])
				if unicode.IsDigit(ch) {
					i++
				} else if ch == '.' && !hasDecimal {
					hasDecimal = true
					i++
				} else {
					break
				}
			}
			// Validate: no letters allowed
			value := expr[start:i]
			if strings.ContainsAny(value, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") {
				return nil, errors.New("invalid characters in number")
			}
			tokens = append(tokens, token{kind: tokenNumber, value: value})
			continue
		}

		// Operators and parentheses
		switch ch {
		case '+':
			tokens = append(tokens, token{kind: tokenPlus, value: "+"})
			i++
		case '-':
			// Check if this is a unary minus (not allowed except at start via 0 handling)
			if len(tokens) == 0 || tokens[len(tokens)-1].kind == tokenLeftParen {
				// Insert 0 for unary minus
				tokens = append(tokens, token{kind: tokenNumber, value: "0"})
			}
			tokens = append(tokens, token{kind: tokenMinus, value: "-"})
			i++
		case '*':
			tokens = append(tokens, token{kind: tokenMultiply, value: "*"})
			i++
		case '/':
			tokens = append(tokens, token{kind: tokenDivide, value: "/"})
			i++
		case '(':
			tokens = append(tokens, token{kind: tokenLeftParen, value: "("})
			i++
		case ')':
			tokens = append(tokens, token{kind: tokenRightParen, value: ")"})
			i++
		case '.':
			// Standalone decimal point is invalid
			return nil, errors.New("invalid decimal point position")
		default:
			return nil, errors.New("invalid character: " + string(ch))
		}
	}

	if hasLeadingMinus {
		// Insert leading 0 if we detected leading minus
		if len(tokens) > 0 && tokens[0].kind == tokenMinus {
			tokens = append([]token{{kind: tokenNumber, value: "0"}}, tokens...)
		}
	}

	tokens = append(tokens, token{kind: tokenEOF, value: ""})
	return tokens, nil
}

func parseExpression(tokens []token, pos int, depth int) (float64, int, error) {
	if depth > maxNestingDepth {
		return 0, pos, errors.New("expression too deeply nested: max 16 levels")
	}

	result, pos, err := parseTerm(tokens, pos, depth)
	if err != nil {
		return 0, pos, err
	}

	for pos < len(tokens) && (tokens[pos].kind == tokenPlus || tokens[pos].kind == tokenMinus) {
		op := tokens[pos].kind
		pos++
		right, newPos, err := parseTerm(tokens, pos, depth)
		if err != nil {
			return 0, pos, err
		}
		if op == tokenPlus {
			result += right
		} else {
			result -= right
		}
		pos = newPos
	}

	return result, pos, nil
}

func parseTerm(tokens []token, pos int, depth int) (float64, int, error) {
	result, pos, err := parseFactor(tokens, pos, depth)
	if err != nil {
		return 0, pos, err
	}

	for pos < len(tokens) && (tokens[pos].kind == tokenMultiply || tokens[pos].kind == tokenDivide) {
		op := tokens[pos].kind
		pos++
		right, newPos, err := parseFactor(tokens, pos, depth)
		if err != nil {
			return 0, pos, err
		}
		if op == tokenMultiply {
			result *= right
		} else {
			if right == 0 {
				return 0, pos, errors.New("division by zero")
			}
			result /= right
		}
		pos = newPos
	}

	return result, pos, nil
}

func parseFactor(tokens []token, pos int, depth int) (float64, int, error) {
	if pos >= len(tokens) {
		return 0, pos, errors.New("unexpected end of expression")
	}

	token := tokens[pos]

	switch token.kind {
	case tokenNumber:
		val, err := strconv.ParseFloat(token.value, 64)
		return val, pos + 1, err

	case tokenLeftParen:
		// Recursive call with increased depth
		result, newPos, err := parseExpression(tokens, pos+1, depth+1)
		if err != nil {
			return 0, newPos, err
		}
		if newPos >= len(tokens) || tokens[newPos].kind != tokenRightParen {
			return 0, newPos, errors.New("missing closing parenthesis")
		}
		return result, newPos + 1, nil

	case tokenRightParen:
		return 0, pos, errors.New("unexpected closing parenthesis")

	case tokenEOF:
		return 0, pos, errors.New("unexpected end of expression")

	default:
		return 0, pos, errors.New("unexpected token: " + token.kind.String())
	}
}

func formatResult(val float64) string {
	// Check if it's a whole number
	if val == float64(int64(val)) {
		return strconv.FormatInt(int64(val), 10)
	}
	// Format with reasonable precision
	return strconv.FormatFloat(val, 'f', -1, 64)
}
