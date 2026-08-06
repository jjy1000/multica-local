package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"strings"
	"testing"
)

const mainSourceFile = "main.go"

// backgroundServiceConstructors must never be called from main(): they build a
// TaskService / AutopilotService that the router has not finished wiring.
var backgroundServiceConstructors = []string{
	"service.NewTaskService",
	"service.NewAutopilotService",
}

// TestMainUsesRouterOwnedBackgroundServices guards the process wiring behind
// scheduled Autopilot dispatch and the runtime sweeper: both must take the
// router's fully wired services off *handler.Handler instead of constructing
// their own.
//
// The router — not NewTaskService — assigns h.TaskService.EmptyClaim
// (router.go). A second TaskService built inside main() therefore has
// EmptyClaim == nil, and because EmptyClaimCache is deliberately nil-safe the
// missed invalidation is silent: a scheduled dispatch still delivers the daemon
// wakeup while the claim path's cached "no queued task" verdict survives, so an
// idle runtime keeps returning empty claims until EmptyClaimCacheTTL expires
// (upstream #6410 / MUL-5747).
//
// This parses main.go instead of asserting on backgroundServices' return
// values. A value-level assertion only proves the helper hands back h's fields,
// which stays true even when main() stops calling it and constructs its own
// services again — i.e. it cannot fail on the exact regression it names.
func TestMainUsesRouterOwnedBackgroundServices(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, mainSourceFile, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", mainSourceFile, err)
	}

	var mainFunc *ast.FuncDecl
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Recv == nil && fn.Name.Name == "main" {
			mainFunc = fn
			break
		}
	}
	if mainFunc == nil || mainFunc.Body == nil {
		t.Fatalf("no func main() with a body found in %s — this guard must be pointed at the real wiring", mainSourceFile)
	}

	// Resolve the variable holding the router's *handler.Handler rather than
	// hardcoding "h": the argument identity is the whole point of the guard,
	// and reading it off the NewRouterWithOptions assignment keeps a rename of
	// that variable from silently weakening the check below.
	routerHandlerVar := routerHandlerIdent(mainFunc.Body)
	if routerHandlerVar == "" {
		t.Fatalf("could not find the *handler.Handler result of NewRouterWithOptions in main() — re-point this guard at the current router wiring")
	}

	forbidden := make(map[string]bool, len(backgroundServiceConstructors))
	for _, name := range backgroundServiceConstructors {
		forbidden[name] = true
	}

	var (
		offenders          []string
		badReuse           []string
		reusesRouter       bool
		registersScheduler bool
	)
	ast.Inspect(mainFunc.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch callee := calleeName(call); {
		case forbidden[callee]:
			offenders = append(offenders, fmt.Sprintf("%s at %s", callee, fset.Position(call.Pos())))
		case callee == "backgroundServices":
			// Matching the callee name alone would accept
			// backgroundServices(nil) — which compiles, reuses nothing, and
			// panics at startup. The argument must be the router's handler.
			if len(call.Args) == 1 {
				if arg, ok := call.Args[0].(*ast.Ident); ok && arg.Name == routerHandlerVar {
					reusesRouter = true
					return true
				}
			}
			badReuse = append(badReuse, fmt.Sprintf("%s at %s", exprText(fset, call), fset.Position(call.Pos())))
		case callee == "scheduler.AutopilotScheduleDispatchJob":
			registersScheduler = true
		}
		return true
	})

	// Anti-vacuity: the scheduled-dispatch registration is what makes the
	// unwired-service hazard reachable. If it moves out of main(), this test
	// no longer covers the path it claims to and must fail rather than pass.
	if !registersScheduler {
		t.Fatalf("scheduler.AutopilotScheduleDispatchJob is no longer registered in main() — re-point this guard at wherever the schedule job is now wired")
	}
	if len(offenders) > 0 {
		t.Errorf("main() constructs its own background services (%s); take them from the router via backgroundServices(%s) so EmptyClaim and the rest of the router wiring come along", strings.Join(offenders, ", "), routerHandlerVar)
	}
	if len(badReuse) > 0 {
		t.Errorf("main() calls backgroundServices with something other than the router handler %q (%s); background workers must reuse the services the router finished wiring", routerHandlerVar, strings.Join(badReuse, ", "))
	}
	if !reusesRouter {
		t.Errorf("main() no longer calls backgroundServices(%s); background workers must reuse the router-owned TaskService/AutopilotService", routerHandlerVar)
	}
}

// routerHandlerIdent returns the name of the variable that receives the
// *handler.Handler from NewRouterWithOptions (the `h` in `r, h := ...`), or ""
// when that assignment is no longer recognizable.
func routerHandlerIdent(body *ast.BlockStmt) string {
	var name string
	ast.Inspect(body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Rhs) != 1 || len(assign.Lhs) != 2 {
			return true
		}
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok || calleeName(call) != "NewRouterWithOptions" {
			return true
		}
		if ident, ok := assign.Lhs[1].(*ast.Ident); ok {
			name = ident.Name
			return false
		}
		return true
	})
	return name
}

// exprText renders an expression back to source for error messages.
func exprText(fset *token.FileSet, expr ast.Expr) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, expr); err != nil {
		return "<unprintable expression>"
	}
	return buf.String()
}

// calleeName renders a call's target as "pkg.Func" or "Func" for matching.
func calleeName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		if pkg, ok := fn.X.(*ast.Ident); ok {
			return pkg.Name + "." + fn.Sel.Name
		}
		return fn.Sel.Name
	}
	return ""
}
