package main

import (
	"bytes"
	"fmt"
	"go/format"
	"go/types"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/lufia/plug/plugcore"
	"golang.org/x/tools/go/ast/astutil"
)

type Stub struct { // Plug?
	f   *File
	fns []*Func
}

func Rewrite(stub *Stub) (string, error) {
	filePath := stub.f.path
	name := filepath.Base(filePath)
	dir := filepath.Join("plug", stub.f.pkg.path)
	if err := os.MkdirAll(dir, 0755); err != nil && !os.IsExist(err) {
		return "", fmt.Errorf("failed to create %s: %w", dir, err)
	}
	file := filepath.Join(dir, name)
	w, err := os.Create(file)
	if err != nil {
		return "", fmt.Errorf("failed to create %s: %w", file, err)
	}
	defer w.Close()

	if err := rewriteFile(w, stub); err != nil {
		return "", fmt.Errorf("failed to rewrite %s: %w", filePath, err)
	}
	if err := w.Sync(); err != nil {
		return "", fmt.Errorf("failed to save a stub: %w", err)
	}
	return file, nil
}

func pkgPath(v any) string {
	return reflect.TypeOf(v).PkgPath()
}

func rewriteFile(w io.Writer, stub *Stub) error {
	fset := stub.f.pkg.c.Fset
	path := pkgPath(plugcore.Object{})
	astutil.AddImport(fset, stub.f.f, path)

	var buf bytes.Buffer
	for _, fn := range stub.fns {
		rewriteFunc(&buf, fn)
	}
	if verbose {
		fmt.Printf("====\n%s\n====\n", buf.Bytes())
	}
	s, err := format.Source(buf.Bytes())
	if err != nil {
		return err
	}
	if err := format.Node(w, fset, stub.f.f); err != nil {
		return err
	}
	fmt.Fprintf(w, "\n%s", s)
	return nil
}

func rewriteFunc(w io.Writer, fn *Func) {
	pkg := fn.pkg.Pkg
	name := fn.fn.Name()
	fn.Rename("_" + name)

	sig := fn.fn.Type().(*types.Signature)
	fmt.Fprint(w, "func ")
	recvName := ""
	if recv := sig.Recv(); recv != nil {
		s := typeStr(recv.Type().Underlying(), pkg)
		fmt.Fprintf(w, "(%s %s) ", recv.Name(), s)
		recvName = recv.Name() + "."
		// TODO(lufia): sig.RecvTypeParams
	}
	fmt.Fprint(w, name)

	var typeParams []string
	if params := sig.TypeParams(); params != nil {
		fmt.Fprint(w, "[")
		typeParams = printTypeParams(w, params, pkg)
		fmt.Fprint(w, "]")
	}

	fmt.Fprint(w, "(")
	paramNames := printVars(w, sig.Params(), pkg)
	fmt.Fprint(w, ") (")
	resultNames := printVars(w, sig.Results(), pkg)
	fmt.Fprintln(w, ") {")
	fmt.Fprintln(w, "\tscope := plugcore.NewScope(1)")
	fmt.Fprintln(w, "\tdefer scope.Delete()")
	if len(typeParams) == 0 {
		fmt.Fprintf(w, "\ts := plugcore.Func(%q, %s_%s)\n", fn.name, recvName, name)
		fmt.Fprintf(w, "\tf := plugcore.Get(scope, s, %s_%s, nil, plugcore.Params{\n", recvName, name)
		recordParams(w, sig.Params())
		fmt.Fprintln(w, "\t})")
	} else {
		s := strings.Join(typeParams, ", ")
		fmt.Fprintf(w, "\ts := plugcore.Func(%q, %s_%s[%s])\n", fn.name, recvName, name, s)
		fmt.Fprintf(w, "\tf := plugcore.Get(scope, s, %s_%s[%s], nil, plugcore.Params{\n", recvName, name, s)
		recordParams(w, sig.Params())
		fmt.Fprintln(w, "\t})")
	}
	if len(resultNames) == 0 {
		fmt.Fprintf(w, "\tf(%s)\n", strings.Join(paramNames, ", "))
	} else {
		fmt.Fprintf(w, "\treturn f(%s)\n", strings.Join(paramNames, ", "))
	}
	fmt.Fprintln(w, "}")
}

func printVars(w io.Writer, vars *types.Tuple, pkg *types.Package) []string {
	if vars == nil {
		return nil
	}
	a := make([]string, vars.Len())
	for i := range vars.Len() {
		v := vars.At(i)
		a[i] = v.Name()
		fmt.Fprintf(w, "%s %s,", v.Name(), typeStr(v.Type(), pkg))
	}
	return a
}

func recordParams(w io.Writer, params *types.Tuple) {
	if params == nil {
		return
	}
	for i := range params.Len() {
		v := params.At(i)
		if v.Name() == "_" {
			continue
		}
		fmt.Fprintf(w, "\t\t%[1]q: %[1]s,\n", v.Name())
	}
}

func printTypeParams(w io.Writer, params *types.TypeParamList, pkg *types.Package) []string {
	a := make([]string, params.Len())
	for i := range params.Len() {
		v := params.At(i)
		a[i] = v.Obj().Name()
		fmt.Fprintf(w, "%s %s,", v.Obj().Name(), typeStr(v.Constraint(), pkg))
	}
	return a
}

func typeStr(t types.Type, pkg *types.Package) string {
	switch v := t.(type) {
	case *types.Named:
		return types.TypeString(v, relativeNameTo(pkg))
	case *types.Pointer:
		return "*" + typeStr(v.Elem(), pkg)
	case *types.Basic:
		return v.Name()
	case *types.Slice:
		return "[]" + typeStr(v.Elem(), pkg)
	default:
		return t.String()
	}
}

func relativeNameTo(pkg *types.Package) types.Qualifier {
	if pkg == nil {
		return nil
	}
	return func(other *types.Package) string {
		if pkg == other {
			return ""
		}
		return other.Name()
	}
}
