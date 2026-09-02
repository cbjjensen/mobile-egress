package main

import (
	"encoding/xml"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"strings"
	"testing"
)

func TestLaunchDaemonPlistSemanticContract(t *testing.T) {
	t.Parallel()

	_, source, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller did not resolve test source")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", ".."))
	plistPath := filepath.Join(repositoryRoot, "windows-client", "macos", "com.cbjjensen.mobile-egress.relay.plist")
	raw, err := os.ReadFile(plistPath)
	if err != nil {
		t.Fatalf("read launchd plist: %v", err)
	}
	root := parseStrictPlistXML(t, raw)
	if root.name != "plist" || !reflect.DeepEqual(root.attributes, map[string]string{"version": "1.0"}) || len(root.children) != 1 {
		t.Fatalf("plist root = %#v", root)
	}
	dictionary := root.children[0]
	if dictionary.name != "dict" || len(dictionary.attributes) != 0 || len(dictionary.children)%2 != 0 {
		t.Fatalf("plist dictionary = %#v", dictionary)
	}
	entries := make(map[string]*plistXMLNode)
	for index := 0; index < len(dictionary.children); index += 2 {
		key := dictionary.children[index]
		value := dictionary.children[index+1]
		if key.name != "key" || len(key.attributes) != 0 || len(key.children) != 0 || strings.TrimSpace(key.text) == "" || key.text != strings.TrimSpace(key.text) {
			t.Fatalf("invalid plist key node = %#v", key)
		}
		if _, duplicate := entries[key.text]; duplicate {
			t.Fatalf("duplicate plist key %q", key.text)
		}
		entries[key.text] = value
	}
	wantKeys := []string{"BundleProgram", "KeepAlive", "Label", "ProcessType", "ProgramArguments", "RunAtLoad", "Sockets", "UserName"}
	gotKeys := make([]string, 0, len(entries))
	for key := range entries {
		gotKeys = append(gotKeys, key)
	}
	slicesSort(gotKeys)
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("plist keys = %v, want %v", gotKeys, wantKeys)
	}
	assertPlistString(t, entries["Label"], "com.cbjjensen.mobile-egress.relay")
	assertPlistString(t, entries["BundleProgram"], "Contents/Resources/mobile-egress-relay")
	assertPlistString(t, entries["UserName"], "root")
	assertPlistString(t, entries["ProcessType"], "Background")
	assertPlistBoolean(t, entries["RunAtLoad"], true)
	assertPlistBoolean(t, entries["KeepAlive"], true)
	assertRelayAdminLaunchSocket(t, entries["Sockets"])
	arguments := entries["ProgramArguments"]
	if arguments.name != "array" || len(arguments.attributes) != 0 || strings.TrimSpace(arguments.text) != "" || len(arguments.children) != 2 {
		t.Fatalf("ProgramArguments = %#v", arguments)
	}
	wantArguments := []string{"Contents/Resources/mobile-egress-relay", "daemon"}
	gotArguments := make([]string, 0, len(arguments.children))
	for _, argument := range arguments.children {
		if argument.name != "string" || len(argument.attributes) != 0 || len(argument.children) != 0 || argument.text != strings.TrimSpace(argument.text) {
			t.Fatalf("invalid ProgramArguments entry = %#v", argument)
		}
		gotArguments = append(gotArguments, argument.text)
	}
	if !reflect.DeepEqual(gotArguments, wantArguments) {
		t.Fatalf("ProgramArguments = %v, want %v", gotArguments, wantArguments)
	}
	for _, argument := range gotArguments {
		lower := strings.ToLower(argument)
		if path.IsAbs(argument) || strings.HasPrefix(argument, "-") || strings.Contains(argument, "=") ||
			strings.Contains(lower, "state") || strings.Contains(lower, "socket") || strings.Contains(lower, "listen") ||
			lower == "sh" || lower == "bash" || lower == "zsh" || lower == "osascript" || lower == "env" {
			t.Fatalf("unsafe launchd argument %q", argument)
		}
	}
}

func assertRelayAdminLaunchSocket(t *testing.T, node *plistXMLNode) {
	t.Helper()
	if node == nil || node.name != "dict" || len(node.attributes) != 0 || len(node.children) != 2 {
		t.Fatalf("Sockets = %#v", node)
	}
	if node.children[0].name != "key" || node.children[0].text != "RelayAdmin" {
		t.Fatalf("Sockets key = %#v", node.children[0])
	}
	entry := node.children[1]
	if entry.name != "dict" || len(entry.attributes) != 0 || len(entry.children) != 8 {
		t.Fatalf("RelayAdmin socket = %#v", entry)
	}
	values := make(map[string]*plistXMLNode)
	for index := 0; index < len(entry.children); index += 2 {
		key := entry.children[index]
		if key.name != "key" || len(key.attributes) != 0 || len(key.children) != 0 || key.text == "" {
			t.Fatalf("RelayAdmin socket key = %#v", key)
		}
		values[key.text] = entry.children[index+1]
	}
	assertPlistString(t, values["SockPathName"], "/var/run/com.cbjjensen.mobile-egress.relay.sock")
	assertPlistInteger(t, values["SockPathOwner"], "0")
	assertPlistInteger(t, values["SockPathGroup"], "80")
	assertPlistInteger(t, values["SockPathMode"], "432")
}

type plistXMLNode struct {
	name       string
	attributes map[string]string
	text       string
	children   []*plistXMLNode
}

func parseStrictPlistXML(t *testing.T, raw []byte) *plistXMLNode {
	t.Helper()
	decoder := xml.NewDecoder(strings.NewReader(string(raw)))
	decoder.Strict = true
	var root *plistXMLNode
	var stack []*plistXMLNode
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("parse launchd plist: %v", err)
		}
		switch value := token.(type) {
		case xml.StartElement:
			node := &plistXMLNode{name: value.Name.Local, attributes: make(map[string]string)}
			if value.Name.Space != "" {
				t.Fatalf("namespaced plist element %q", value.Name)
			}
			for _, attribute := range value.Attr {
				if attribute.Name.Space != "" || attribute.Name.Local == "" {
					t.Fatalf("invalid plist attribute %#v", attribute)
				}
				if _, duplicate := node.attributes[attribute.Name.Local]; duplicate {
					t.Fatalf("duplicate attribute %q", attribute.Name.Local)
				}
				node.attributes[attribute.Name.Local] = attribute.Value
			}
			if len(stack) == 0 {
				if root != nil {
					t.Fatal("multiple plist roots")
				}
				root = node
			} else {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, node)
			}
			stack = append(stack, node)
		case xml.EndElement:
			if len(stack) == 0 || stack[len(stack)-1].name != value.Name.Local {
				t.Fatalf("unbalanced plist end element %q", value.Name.Local)
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 0 {
				if strings.TrimSpace(string(value)) != "" {
					t.Fatalf("text outside plist root %q", value)
				}
				continue
			}
			stack[len(stack)-1].text += string(value)
		case xml.ProcInst:
			if value.Target != "xml" {
				t.Fatalf("unexpected processing instruction %q", value.Target)
			}
		case xml.Directive:
			if !strings.HasPrefix(strings.TrimSpace(string(value)), "DOCTYPE plist ") {
				t.Fatalf("unexpected XML directive %q", value)
			}
		case xml.Comment:
			t.Fatal("comments are not permitted in the tracked launchd plist")
		default:
			t.Fatalf("unexpected plist token %T", token)
		}
	}
	if root == nil || len(stack) != 0 {
		t.Fatal("incomplete launchd plist")
	}
	return root
}

func assertPlistString(t *testing.T, node *plistXMLNode, want string) {
	t.Helper()
	if node == nil || node.name != "string" || len(node.attributes) != 0 || len(node.children) != 0 || node.text != want {
		t.Fatalf("plist string = %#v, want %q", node, want)
	}
}

func assertPlistBoolean(t *testing.T, node *plistXMLNode, want bool) {
	t.Helper()
	wantName := "false"
	if want {
		wantName = "true"
	}
	if node == nil || node.name != wantName || len(node.attributes) != 0 || len(node.children) != 0 || strings.TrimSpace(node.text) != "" {
		t.Fatalf("plist boolean = %#v, want %t", node, want)
	}
}

func assertPlistInteger(t *testing.T, node *plistXMLNode, want string) {
	t.Helper()
	if node == nil || node.name != "integer" || len(node.attributes) != 0 || len(node.children) != 0 || node.text != want {
		t.Fatalf("plist integer = %#v, want %q", node, want)
	}
}

func slicesSort(values []string) {
	for index := 1; index < len(values); index++ {
		for current := index; current > 0 && values[current] < values[current-1]; current-- {
			values[current], values[current-1] = values[current-1], values[current]
		}
	}
}
