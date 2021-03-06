package parser

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	GenerateFileSuffix = "_zenrpc.go"

	zenrpcComment     = "//zenrpc"
	zenrpcService     = "zenrpc.Service"
	contextTypeName   = "context.Context"
	errorTypeName     = "zenrpc.Error"
	testFileSuffix    = "_test.go"
	goFileSuffix      = ".go"
	zenrpcMagicPrefix = "//zenrpc:"
)

// PackageInfo represents struct info for XXX_zenrpc.go file generation
type PackageInfo struct {
	Dir string

	PackageName string
	Services    []*Service

	Scopes  map[string][]*ast.Scope // key - import name, value - array of scopes from each package file
	Structs map[string]*Struct
	Imports []*ast.ImportSpec

	StructsNamespacesFromArgs map[string]struct{} // set of structs names from arguments for printing imports
	ImportsForGeneration      []*ast.ImportSpec

	caser    func(string) string
	argCaser func(string) string
}

type Service struct {
	GenDecl     *ast.GenDecl
	Name        string
	Methods     []*Method
	Description string
	IsInterface bool
}

type Method struct {
	FuncDecl           *ast.FuncType
	Name               string
	EndpointName       string
	SchemaEndpointName string
	HasContext         bool
	Args               []Arg
	DefaultValues      map[string]DefaultValue
	Returns            []Return
	SMDReturn          *SMDReturn // return for generate smd schema; pointer for nil check
	Description        string

	Errors []SMDError // errors for documentation in SMD
}

type DefaultValue struct {
	Name        string
	CapitalName string
	Type        string // without star
	Comment     string // original comment
	Value       string
}

type Arg struct {
	Name            string
	Type            string
	CapitalName     string
	CaseName        string
	HasStar         bool
	HasDefaultValue bool
	Description     string // from magic comment
	SMDType         SMDType
}

func (a Arg) String() string {
	return strings.Join([]string{a.Name, a.Type}, " ")
}

type Return struct {
	Name string
	Type string
}

type SMDReturn struct {
	Name        string
	HasStar     bool
	Description string
	SMDType     SMDType
}

type Struct struct {
	Name       string // key in map, Ref in arguments and returns
	Namespace  string
	Type       string
	StructType *ast.StructType
	Properties []Property // array because order is important
}

type Interface struct {
	Name          string // key in map, Ref in arguments and returns
	Namespace     string
	Type          string
	InterfaceType *ast.InterfaceType
	Properties    []Property // array because order is important
}

type Property struct {
	Name        string
	Description string
	SMDType     SMDType
}

// SMDType is a type representation for SMD generation
type SMDType struct {
	Type      string
	ItemsType string // for array
	Ref       string // for object and also if array item is object
}

type SMDError struct {
	Code        int
	Description string
}

func NewPackageInfo(useSnakeCase bool) *PackageInfo {
	caser := toLowerCase
	argCaser := toNoCase
	if useSnakeCase {
		caser = toSnakeCase
		argCaser = toSnakeCase
	}
	return &PackageInfo{
		Services: []*Service{},

		Scopes:  make(map[string][]*ast.Scope),
		Structs: make(map[string]*Struct),
		Imports: []*ast.ImportSpec{},

		StructsNamespacesFromArgs: make(map[string]struct{}),
		ImportsForGeneration:      []*ast.ImportSpec{},

		caser:    caser,
		argCaser: argCaser,
	}
}

// ParseFiles parse all files associated with package from original file
func (pi *PackageInfo) Parse(filename string) error {
	if dir, err := filepath.Abs(filepath.Dir(filename)); err != nil {
		return err
	} else {
		pi.Dir = dir
	}

	files, err := ioutil.ReadDir(pi.Dir)
	if err != nil {
		return err
	}

	if err := pi.parseFiles(files); err != nil {
		return err
	}

	// collect scopes from imported packages
	pi.Imports = uniqueImports(pi.Imports)
	pi.ImportsForGeneration = filterImports(pi.Imports, pi.StructsNamespacesFromArgs)
	pi.Imports = filterImports(pi.Imports, uniqueStructsNamespaces(pi.Structs))
	if err := pi.parseImports(pi.Imports); err != nil {
		return err
	}

	pi.parseStructs()

	return nil
}

func (pi *PackageInfo) parseFiles(files []os.FileInfo) error {
	var astFiles []*ast.File
	// first loop: parse files and structs
	for _, f := range files {
		if f.IsDir() {
			continue
		}

		if !strings.HasSuffix(f.Name(), goFileSuffix) ||
			strings.HasSuffix(f.Name(), GenerateFileSuffix) || strings.HasSuffix(f.Name(), testFileSuffix) {
			continue
		}

		astFile, err := parser.ParseFile(token.NewFileSet(), filepath.Join(pi.Dir, f.Name()), nil, parser.ParseComments)
		if err != nil {
			return err
		}

		// for debug
		//ast.Print(fset, astFile)

		if len(pi.PackageName) == 0 {
			pi.PackageName = astFile.Name.Name
		} else if pi.PackageName != astFile.Name.Name {
			continue
		}

		// get structs for zenrpc
		err = pi.parseServices(astFile)
		if err != nil {
			return err
		}

		pi.Scopes["."] = append(pi.Scopes["."], astFile.Scope) // collect current package scopes
		pi.Imports = append(pi.Imports, astFile.Imports...)    // collect imports

		astFiles = append(astFiles, astFile)
	}

	// second loop: parse methods
	for _, f := range astFiles {
		if err := pi.parseMethods(f); err != nil {
			return err
		}
	}

	return nil
}

func (pi *PackageInfo) parseServices(f *ast.File) error {
	for _, decl := range f.Decls {
		gdecl, ok := decl.(*ast.GenDecl)
		if !ok || gdecl.Tok != token.TYPE {
			continue
		}

		for _, spec := range gdecl.Specs {
			spec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}

			if !ast.IsExported(spec.Name.Name) {
				continue
			}

			switch tt := spec.Type.(type) {
			case *ast.StructType:
				// check that struct is our zenrpc struct
				if hasZenrpcComment(spec, zenrpcComment) || hasZenrpcService(tt) {
					pi.Services = append(pi.Services, &Service{
						GenDecl:     gdecl,
						Name:        spec.Name.Name,
						Methods:     []*Method{},
						Description: parseCommentGroup(spec.Doc),
					})
				}
			case *ast.InterfaceType:
				if hasZenrpcComment(spec, zenrpcComment) {
					methods, err := pi.parseInterfaceMethods(tt)
					if err != nil {
						return err
					}
					pi.Services = append(pi.Services, &Service{
						GenDecl:     gdecl,
						Name:        spec.Name.Name,
						Methods:     methods,
						Description: parseCommentGroup(spec.Doc),
						IsInterface: true,
					})
				}
			default:
				continue
			}
		}
	}
	return nil
}

func (pi *PackageInfo) parseInterfaceMethods(it *ast.InterfaceType) ([]*Method, error) {
	if it.Methods == nil {
		return nil, nil
	}
	var mm []*Method
	for _, method := range it.Methods.List {
		ff, ok := method.Type.(*ast.FuncType)
		if !ok { // then embedded
			continue
		}
		m := Method{
			FuncDecl:           ff,
			Name:               method.Names[0].Name,
			EndpointName:       pi.caser(method.Names[0].Name),
			SchemaEndpointName: pi.argCaser(method.Names[0].Name),
			Args:               []Arg{},
			DefaultValues:      make(map[string]DefaultValue),
			Returns:            []Return{},
			Description:        parseCommentGroup(method.Doc),
			Errors:             []SMDError{},
		}

		serviceNames := []string{m.Name}

		if err := m.parseArguments(pi, ff, serviceNames); err != nil {
			return nil, err
		}

		if err := m.parseReturns(pi, ff, serviceNames); err != nil {
			return nil, err
		}

		// parse default values
		m.parseComments(method.Doc, pi)

		mm = append(mm, &m)
	}
	return mm, nil
}

func (pi *PackageInfo) parseMethods(f *ast.File) error {
	for _, decl := range f.Decls {
		fdecl, ok := decl.(*ast.FuncDecl)
		if !ok || fdecl.Recv == nil {
			continue
		}

		m := Method{
			FuncDecl:           fdecl.Type,
			Name:               fdecl.Name.Name,
			EndpointName:       pi.caser(fdecl.Name.Name),
			SchemaEndpointName: pi.argCaser(fdecl.Name.Name),
			Args:               []Arg{},
			DefaultValues:      make(map[string]DefaultValue),
			Returns:            []Return{},
			Description:        parseCommentGroup(fdecl.Doc),
			Errors:             []SMDError{},
		}

		serviceNames := m.linkWithServices(pi, fdecl)

		// services not found
		if len(serviceNames) == 0 {
			continue
		}

		if err := m.parseArguments(pi, fdecl.Type, serviceNames); err != nil {
			return err
		}

		if err := m.parseReturns(pi, fdecl.Type, serviceNames); err != nil {
			return err
		}

		// parse default values
		m.parseComments(fdecl.Doc, pi)
	}

	return nil
}

func (pi PackageInfo) String() string {
	result := fmt.Sprintf("Generated services for package %s:\n", pi.PackageName)
	for _, s := range pi.Services {
		result += fmt.Sprintf("- %s\n", s.Name)
		for _, m := range s.Methods {
			result += fmt.Sprintf("  • %s\n", m)
		}
	}

	return result
}

func (m Method) String() string {
	var argsStr []string
	for _, a := range m.Args {
		argsStr = append(argsStr, a.String())
	}
	result := m.Name + "(" + strings.Join(argsStr, ", ") + ")"

	// no return args
	if len(m.Returns) == 0 {
		return result
	}
	result += " "
	// only one return arg without name
	if len(m.Returns) == 1 && len(m.Returns[0].Name) == 0 {
		result += m.Returns[0].Type
		return result
	}

	// return
	var returnStr []string
	for _, a := range m.Returns {
		if len(a.Name) == 0 {
			returnStr = append(returnStr, a.Type)
		} else {
			returnStr = append(returnStr, fmt.Sprintf("%s %s", a.Name, a.Type))
		}
	}
	result += "(" + strings.Join(returnStr, ", ") + ")"
	return result
}

// HasErrorVariable define adding err variable to generated Invoke function
func (s Service) HasErrorVariable() bool {
	for _, m := range s.Methods {
		if len(m.Args) > 0 {
			return true
		}
	}
	return false
}

// linkWithServices add method for services
func (m *Method) linkWithServices(pi *PackageInfo, fdecl *ast.FuncDecl) (names []string) {
	for _, field := range fdecl.Recv.List {
		// field can be pointer or not
		var ident *ast.Ident
		if starExpr, ok := field.Type.(*ast.StarExpr); ok {
			if ident, ok = starExpr.X.(*ast.Ident); !ok {
				continue
			}
		} else if ident, ok = field.Type.(*ast.Ident); !ok {
			continue
		}

		if !ast.IsExported(fdecl.Name.Name) {
			continue
		}

		// find service in our service list
		// method can be in several services //fixme: Any example? Because https://golang.org/ref/spec#Method_declarations tells opposite
		for _, s := range pi.Services {
			if s.Name == ident.Name {
				names = append(names, s.Name)
				s.Methods = append(s.Methods, m)
				break
			}
		}
	}

	return
}

func (m *Method) parseArguments(pi *PackageInfo, fdecl *ast.FuncType, serviceNames []string) error {
	if fdecl.Params == nil || fdecl.Params.List == nil {
		return nil
	}

	for _, field := range fdecl.Params.List {
		if field.Names == nil {
			continue
		}

		// parse type
		typeName := parseType(field.Type)
		if typeName == "" {
			// get argument names
			var fields []string
			for _, name := range field.Names {
				fields = append(fields, name.Name)
			}

			// get Service.Method list
			var methods []string
			for _, s := range serviceNames {
				methods = append(methods, s+"."+m.Name)
			}
			return fmt.Errorf("can't parse type of argument %s in %s", strings.Join(fields, ", "), strings.Join(methods, ", "))
		}

		if typeName == contextTypeName {
			m.HasContext = true
			continue // not add context to arg list
		}

		hasStar := hasStar(typeName) // check for pointer
		smdType, itemType := parseSMDType(field.Type)

		// find and save struct
		s := parseStruct(field.Type)
		var ref string
		if s != nil {
			ref = s.Name

			// collect namespaces (imports)
			if s.Namespace != "" {
				if _, ok := pi.StructsNamespacesFromArgs[s.Namespace]; !ok {
					pi.StructsNamespacesFromArgs[s.Namespace] = struct{}{}
				}
			}

			if currentS, ok := pi.Structs[s.Name]; !ok || (currentS.StructType == nil && s.StructType != nil) {
				pi.Structs[s.Name] = s
			}
		}

		// parse names
		for _, name := range field.Names {
			m.Args = append(m.Args, Arg{
				Name:        name.Name,
				Type:        typeName,
				CapitalName: strings.Title(name.Name),
				CaseName:    pi.argCaser(name.Name),
				HasStar:     hasStar,
				SMDType: SMDType{
					Type:      smdType,
					ItemsType: itemType,
					Ref:       ref,
				},
			})
		}
	}

	return nil
}

func (m *Method) parseReturns(pi *PackageInfo, fdecl *ast.FuncType, serviceNames []string) error {
	if fdecl.Results == nil || fdecl.Results.List == nil {
		return nil
	}

	// get Service.Method list
	methodsFn := func() string {
		var methods []string
		for _, s := range serviceNames {
			methods = append(methods, s+"."+m.Name)
		}
		return strings.Join(methods, ", ")
	}

	hasError := false
	for _, field := range fdecl.Results.List {
		if len(field.Names) > 1 {
			return fmt.Errorf("%s contain more than one return arguments with same type", methodsFn())
		}

		// parse type
		typeName := parseType(field.Type)
		if typeName == "" {
			return fmt.Errorf("can't parse type of return value in %s on position %d", methodsFn(), len(m.Returns)+1)
		}

		var fieldName string
		// get names if exist
		if field.Names != nil {
			fieldName = field.Names[0].Name
		}

		m.Returns = append(m.Returns, Return{
			Type: typeName,
			Name: fieldName,
		})

		if typeName == "error" || typeName == errorTypeName || typeName == "*"+errorTypeName {
			if hasError {
				return fmt.Errorf("%s contain more than one error return arguments", methodsFn())
			}
			hasError = true
			continue
		}

		if m.SMDReturn != nil {
			return fmt.Errorf("%s contain more than one valuable return argument", methodsFn())
		}

		hasStar := hasStar(typeName) // check for pointer
		smdType, itemType := parseSMDType(field.Type)

		// find and save struct
		s := parseStruct(field.Type)
		var ref string
		if s != nil {
			ref = s.Name

			if currentS, ok := pi.Structs[s.Name]; !ok || (currentS.StructType == nil && s.StructType != nil) {
				pi.Structs[s.Name] = s
			}
		}

		m.SMDReturn = &SMDReturn{
			Name:    fieldName,
			HasStar: hasStar,
			SMDType: SMDType{
				Type:      smdType,
				ItemsType: itemType,
				Ref:       ref,
			},
		}
	}

	return nil
}

// parseComments parse method comments and
// fill default values, description for params and user errors map
func (m *Method) parseComments(doc *ast.CommentGroup, pi *PackageInfo) {
	if doc == nil {
		return
	}

	for _, comment := range doc.List {
		if strings.HasPrefix(comment.Text, zenrpcMagicPrefix) {
			m.parseMagicOption(comment, pi)
		}
	}
}

func (m *Method) parseMagicOption(comment *ast.Comment, pi *PackageInfo) {
	// split by magic path and description
	fields := strings.Fields(comment.Text)
	couple := [...]string{
		strings.TrimPrefix(strings.TrimSpace(fields[0]), zenrpcMagicPrefix),
		strings.Join(fields[1:], " "),
	}

	// parse arguments
	if args := strings.Split(couple[0], "="); len(args) == 2 {
		// default value
		// example: "//zenrpc:exp=2 	exponent could be empty"

		name := args[0]
		value := args[1]
		for i, a := range m.Args {
			if a.Name == name {
				m.DefaultValues[name] = DefaultValue{
					Name:        name,
					CapitalName: a.CapitalName,
					Type:        strings.TrimPrefix(a.Type, "*"), // remove star
					Comment:     comment.Text,
					Value:       value,
				}

				m.Args[i].HasDefaultValue = true
				if len(couple) == 2 {
					m.Args[i].Description = couple[1]
				}

				break
			}
		}
	} else if couple[0] == "return" {
		// description for return
		// example: "//zenrpc:return operation result"

		m.SMDReturn.Description = couple[1]
	} else if i, err := strconv.Atoi(couple[0]); err == nil {
		// error code
		// example: "//zenrpc:-32603		divide by zero"

		m.Errors = append(m.Errors, SMDError{i, couple[1]})
	} else {
		// description for argument without default value
		// example: "//zenrpc:id person id"

		for i, a := range m.Args {
			if a.Name == couple[0] {
				m.Args[i].Description = couple[1]
				break
			}
		}
	}
}

func parseCommentGroup(doc *ast.CommentGroup) string {
	if doc == nil {
		return ""
	}

	result := ""
	for _, comment := range doc.List {
		if strings.HasPrefix(comment.Text, zenrpcComment) {
			continue
		}

		if len(result) > 0 {
			result += "\n"
		}
		result += strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))
	}

	return result
}

func parseType(expr ast.Expr) string {
	switch v := expr.(type) {
	case *ast.StarExpr:
		return "*" + parseType(v.X)
	case *ast.SelectorExpr:
		return parseType(v.X) + "." + v.Sel.Name
	case *ast.ArrayType:
		return "[" + parseType(v.Len) + "]" + parseType(v.Elt)
	case *ast.MapType:
		return "map[" + parseType(v.Key) + "]" + parseType(v.Value)
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.Ident:
		return v.Name
	case *ast.BasicLit:
		// for array size
		return v.Value
	default:
		return ""
	}
}

func parseSMDType(expr ast.Expr) (string, string) {
	switch v := expr.(type) {
	case *ast.StarExpr:
		return parseSMDType(v.X)
	case *ast.SelectorExpr:
		if v.X.(*ast.Ident).Name == "time" && v.Sel.Name == "Time" {
			return "String", ""
		}
		return "Object", ""
	case *ast.MapType, *ast.InterfaceType:
		return "Object", ""
	case *ast.ArrayType:
		mainType, itemType := parseSMDType(v.Elt)
		if itemType != "" {
			return "Array", itemType
		}

		return "Array", mainType
	case *ast.Ident:
		switch v.Name {
		case "bool":
			return "Boolean", ""
		case "string":
			return "String", ""
		case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr", "byte", "rune":
			return "Integer", ""
		case "float32", "float64", "complex64", "complex128":
			return "Float", ""
		default:
			return "Object", "" // *ast.Ident contain type name, if type not basic then it struct or alias
		}
	default:
		return "Object", "" // default complex type is object
	}
}

// parseStruct find struct in type for display in SMD
func parseStruct(expr ast.Expr) *Struct {
	switch v := expr.(type) {
	case *ast.StarExpr:
		return parseStruct(v.X)
	case *ast.SelectorExpr:
		namespace := v.X.(*ast.Ident).Name
		if namespace == "time" && v.Sel.Name == "Time" {
			return nil
		}

		return &Struct{
			Name:      namespace + "." + v.Sel.Name,
			Namespace: namespace,
			Type:      v.Sel.Name,
		}
	case *ast.ArrayType:
		// will get last type
		return parseStruct(v.Elt)
	case *ast.MapType:
		// will get last type
		return parseStruct(v.Value)
	case *ast.Ident:
		switch v.Name {
		case "bool", "string",
			"int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr", "byte", "rune",
			"float32", "float64", "complex64", "complex128":
			return nil
		}

		s := &Struct{
			Name:      v.Name,
			Namespace: ".",
			Type:      v.Name,
		}

		if v.Obj != nil && v.Obj.Decl != nil {
			if ts, ok := v.Obj.Decl.(*ast.TypeSpec); ok {
				if st, ok := ts.Type.(*ast.StructType); ok {
					s.StructType = st
				}
			}
		}

		return s
	default:
		return nil
	}
}

func hasZenrpcComment(spec *ast.TypeSpec, comment string) bool {
	return hasComment(spec.Comment, comment) || hasComment(spec.Doc, comment)
}

func hasComment(group *ast.CommentGroup, text string) bool {
	if group == nil {
		return false
	}
	for _, line := range group.List {
		if line.Text == text {
			return true
		}
	}
	return false
}

func hasZenrpcService(structType *ast.StructType) bool {
	if structType.Fields.List == nil {
		return false
	}

	for _, field := range structType.Fields.List {
		selectorExpr, ok := field.Type.(*ast.SelectorExpr)
		if !ok {
			continue
		}

		x, ok := selectorExpr.X.(*ast.Ident)
		if ok && selectorExpr.Sel != nil && x.Name+"."+selectorExpr.Sel.Name == zenrpcService {
			return true
		}
	}

	return false
}

func hasStar(s string) bool {
	if s[:1] == "*" {
		return true
	}

	return false
}
