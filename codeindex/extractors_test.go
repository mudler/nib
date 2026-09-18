package codeindex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustIndex(t *testing.T, path string) string {
	t.Helper()
	out, err := Index(path)
	if err != nil {
		t.Fatalf("Index(%s) failed: %v", path, err)
	}
	return out
}

func assertContains(t *testing.T, out, want, label string) {
	t.Helper()
	if !strings.Contains(out, want) {
		t.Fatalf("%s: expected %q in output:\n%s", label, want, out)
	}
}

func assertNotContains(t *testing.T, out, want, label string) {
	t.Helper()
	if strings.Contains(out, want) {
		t.Fatalf("%s: did not expect %q in output:\n%s", label, want, out)
	}
}

// ---- Python ----

const samplePython = `import os
import sys
from typing import List

MAX_RETRIES = 3
DEFAULT_NAME = "test"

class Config:
    def __init__(self, name: str):
        self.name = name

    def get_name(self) -> str:
        return self.name

class Animal(Dog):
    def speak(self) -> str:
        return "woof"

def main():
    print("hello")

def process(data: bytes) -> int:
    return 0

@decorator
def decorated():
    pass
`

func TestIndexPython(t *testing.T) {
	path := writeTempFile(t, "sample.py", samplePython)
	out := mustIndex(t, path)

	assertContains(t, out, "Import:", "python imports")
	assertContains(t, out, "Constant: MAX_RETRIES", "python constant")
	assertContains(t, out, "Class: Config", "python class")
	assertContains(t, out, "__init__(self, name: str)", "python method")
	assertContains(t, out, "get_name(self) -> str", "python method return type")
	assertContains(t, out, "Class: Config", "python class detail")
	assertContains(t, out, "Class: Animal", "python subclass")
	assertContains(t, out, "Function: main()", "python function")
	assertContains(t, out, "Function: process(data: bytes) -> int", "python function with return type")
	assertContains(t, out, "Function: decorated()", "python decorated function")
}

// ---- Rust ----

const sampleRust = `use std::collections::HashMap;
extern crate serde;

const MAX_SIZE: usize = 1024;
static VERSION: &str = "1.0";

pub struct Config {
    name: String,
    size: usize,
}

pub enum Status {
    Active,
    Inactive,
}

pub trait Handler {
    fn handle(&self, req: &Request) -> Result<(), Error>;
}

pub fn main() {
    println!("hello");
}

pub fn process(data: &[u8]) -> Result<usize, Error> {
    Ok(0)
}

impl Config {
    pub fn new(name: String) -> Self {
        Self { name }
    }
}

pub mod network {
    pub fn connect() {}
}
`

func TestIndexRust(t *testing.T) {
	path := writeTempFile(t, "sample.rs", sampleRust)
	out := mustIndex(t, path)

	assertContains(t, out, "Import: use std::collections::HashMap", "rust use")
	assertContains(t, out, "extern crate serde", "rust extern crate")
	assertContains(t, out, "Constant: MAX_SIZE", "rust const")
	assertContains(t, out, "Variable: VERSION", "rust static")
	assertContains(t, out, "Type: Config struct", "rust struct")
	assertContains(t, out, "Type: Status enum", "rust enum")
	assertContains(t, out, "Trait: Handler", "rust trait")
	assertContains(t, out, "handle(&self, req: &Request) -> Result<(), Error>", "rust trait method")
	assertContains(t, out, "Function: main()", "rust function")
	assertContains(t, out, "Function: process(data: &[u8]) -> Result<usize, Error>", "rust function")
	assertContains(t, out, "Impl: Config", "rust impl")
	assertContains(t, out, "new(name: String) -> Self", "rust impl method")
	assertContains(t, out, "Module: network", "rust module")
}

// ---- TypeScript ----

const sampleTypeScript = `import { foo } from "bar";
import * as baz from "baz";

export interface Handler {
    handle(req: Request): void;
    close(): void;
}

export type Status = "active" | "inactive";

export class Config {
    name: string;

    constructor(name: string) {}

    getName(): string {
        return this.name;
    }
}

export function main(): void {
    console.log("hello");
}

export const MAX: number = 42;

export enum Color {
    Red,
    Green,
    Blue,
}
`

func TestIndexTypeScript(t *testing.T) {
	path := writeTempFile(t, "sample.ts", sampleTypeScript)
	out := mustIndex(t, path)

	assertContains(t, out, `Import: import { foo } from "bar"`, "ts import")
	assertContains(t, out, "Trait: Handler", "ts interface")
	assertContains(t, out, "handle(req: Request): void", "ts interface method")
	assertContains(t, out, "Type: Status", "ts type alias")
	assertContains(t, out, "Class: Config", "ts class")
	assertContains(t, out, "getName(): string", "ts class method")
	assertContains(t, out, "Function: main(): void", "ts function")
	assertContains(t, out, "Constant: MAX", "ts const")
	assertContains(t, out, "Color enum", "ts enum")
}

// ---- JavaScript ----

const sampleJavaScript = `import { foo } from "bar";

export class Config {
    constructor(name) {}

    getName() {
        return this.name;
    }
}

export function main() {
    console.log("hello");
}

export const MAX = 42;
`

func TestIndexJavaScript(t *testing.T) {
	path := writeTempFile(t, "sample.js", sampleJavaScript)
	out := mustIndex(t, path)

	assertContains(t, out, `Import: import { foo } from "bar"`, "js import")
	assertContains(t, out, "Class: Config", "js class")
	assertContains(t, out, "getName()", "js class method")
	assertContains(t, out, "Function: main()", "js function")
	assertContains(t, out, "Constant: MAX", "js const")
}

// ---- TSX ----

const sampleTSX = `import React from "react";

export interface Props {
    name: string;
    count: number;
}

export function Card(props: Props): JSX.Element {
    return <div>{props.name}</div>;
}

export class App extends React.Component {
    render(): JSX.Element {
        return <h1>Hello</h1>;
    }
}

export const MAX_ITEMS = 10;
`

func TestIndexTSX(t *testing.T) {
	path := writeTempFile(t, "sample.tsx", sampleTSX)
	out := mustIndex(t, path)

	assertContains(t, out, `Import: import React from "react"`, "tsx import")
	assertContains(t, out, "Trait: Props", "tsx interface")
	assertContains(t, out, "name: string", "tsx interface property")
	assertContains(t, out, "Function: Card(props: Props): JSX.Element", "tsx function")
	assertContains(t, out, "Class: App", "tsx class")
	assertContains(t, out, "render(): JSX.Element", "tsx class method")
	assertContains(t, out, "Constant: MAX_ITEMS", "tsx const")
}

// ---- C ----

const sampleC = `#include <stdio.h>
#include <stdlib.h>

#define MAX_SIZE 1024
#define MIN(a, b) ((a) < (b) ? (a) : (b))

typedef struct {
    int x;
    int y;
} Point;

typedef int (*compare_fn)(const void *, const void *);

struct Config {
    char* name;
    int size;
};

enum Status {
    ACTIVE,
    INACTIVE,
};

int main(int argc, char** argv) {
    return 0;
}

void process(int* data, size_t len) {
    // ...
}
`

func TestIndexC(t *testing.T) {
	path := writeTempFile(t, "sample.c", sampleC)
	out := mustIndex(t, path)

	assertContains(t, out, "Import: #include <stdio.h>", "c include")
	assertContains(t, out, "#include <stdlib.h>", "c include 2")
	assertContains(t, out, "Constant: #define MAX_SIZE 1024", "c define")
	assertContains(t, out, "MIN(a, b)", "c macro function")
	assertContains(t, out, "Type: Point", "c typedef struct")
	assertContains(t, out, "Type: compare_fn", "c typedef function ptr")
	assertContains(t, out, "Type: Config struct", "c struct")
	assertContains(t, out, "Type: Status enum", "c enum")
	assertContains(t, out, "Function: main(int argc, char** argv)", "c function")
	assertContains(t, out, "Function: process(int* data, size_t len)", "c function 2")
}

// ---- Java ----

const sampleJava = `package com.example;

import java.util.List;
import java.util.Map;

public class Config {
    private String name;

    public Config(String name) {
        this.name = name;
    }

    public String getName() {
        return name;
    }
}

interface Handler {
    void handle(Request req);
    void close();
}

enum Status {
    ACTIVE,
    INACTIVE
}

public record Point(int x, int y) {}
`

func TestIndexJava(t *testing.T) {
	path := writeTempFile(t, "sample.java", sampleJava)
	out := mustIndex(t, path)

	assertContains(t, out, "Package: com.example", "java package")
	assertContains(t, out, "Import: import java.util.List", "java import")
	assertContains(t, out, "import java.util.Map", "java import 2")
	assertContains(t, out, "Class: Config", "java class")
	assertContains(t, out, "Config(String name)", "java constructor")
	assertContains(t, out, "getName()", "java method")
	assertContains(t, out, "Trait: Handler", "java interface")
	assertContains(t, out, "handle(Request req)", "java interface method")
	assertContains(t, out, "Type: Status enum", "java enum")
	assertContains(t, out, "Class: Point", "java record")
	assertContains(t, out, "int x, int y", "java record params")
}

// ---- Ruby ----

const sampleRuby = `require 'json'
require_relative 'helper'

class Config
  def initialize(name)
    @name = name
  end

  def get_name
    @name
  end
end

module Network
  def self.connect
    # ...
  end
end

def main
  puts "hello"
end

MAX_RETRIES = 3
`

func TestIndexRuby(t *testing.T) {
	path := writeTempFile(t, "sample.rb", sampleRuby)
	out := mustIndex(t, path)

	assertContains(t, out, "Import: require 'json'", "ruby require")
	assertContains(t, out, "require_relative 'helper'", "ruby require_relative")
	assertContains(t, out, "Class: Config", "ruby class")
	assertContains(t, out, "initialize(name)", "ruby method")
	assertContains(t, out, "get_name", "ruby method 2")
	assertContains(t, out, "Module: Network", "ruby module")
	assertContains(t, out, "self.connect", "ruby singleton method")
	assertContains(t, out, "Function: main", "ruby function")
	assertContains(t, out, "Constant: MAX_RETRIES", "ruby constant")
}

// ---- Bash ----

const sampleBash = `#!/bin/bash

function greet() {
    echo "hello"
}

main() {
    echo "main"
}

NAME="test"
`

func TestIndexBash(t *testing.T) {
	path := writeTempFile(t, "sample.sh", sampleBash)
	out := mustIndex(t, path)

	assertContains(t, out, "Function: greet()", "bash function keyword")
	assertContains(t, out, "Function: main()", "bash function parens")
	assertContains(t, out, "Variable: NAME", "bash variable")
}

// ---- Cross-language tests ----

func TestAllSupportedExtensions(t *testing.T) {
	exts := SupportedExtensions()
	want := []string{".go", ".py", ".rs", ".ts", ".tsx", ".js", ".c", ".java", ".rb", ".sh"}
	extSet := make(map[string]bool)
	for _, e := range exts {
		extSet[e] = true
	}
	for _, w := range want {
		if !extSet[w] {
			t.Errorf("expected extension %s in supported extensions: %v", w, exts)
		}
	}
}

func TestSyntaxErrorResilience(t *testing.T) {
	tests := []struct {
		name string
		ext  string
		src  string
		want string
	}{
		{"python", ".py", "def good():\n  pass\ndef broken(\n", "Function: good"},
		{"rust", ".rs", "fn good() {}\nfn broken( {\n}\n", "Function: good"},
		{"typescript", ".ts", "function good() {}\nfunction broken( {\n}\n", "Function: good"},
		{"c", ".c", "int good() { return 0; }\nint broken( { }\n", "Function: good"},
		{"java", ".java", "class Good { }\nclass Broken { {\n", "Class: Good"},
		{"ruby", ".rb", "def good\nend\ndef broken(\n", "Function: good"},
		{"bash", ".sh", "good() { :; }\nbroken( {\n", "Function: good"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTempFile(t, "broken"+tc.ext, tc.src)
			out, err := Index(path)
			if err != nil {
				t.Fatalf("Index should not fail on syntax errors: %v", err)
			}
			assertContains(t, out, tc.want, tc.name+" syntax error resilience")
		})
	}
}

func TestPythonDecoratedFunction(t *testing.T) {
	src := `@app.route("/")
def handler():
    pass

@dataclass
class Point:
    x: int
    y: int
`
	path := writeTempFile(t, "test.py", src)
	out := mustIndex(t, path)
	assertContains(t, out, "Function: handler()", "python decorated function")
	assertContains(t, out, "Class: Point", "python decorated class")
}

func TestRustImplBlockMethods(t *testing.T) {
	src := `struct Foo;

impl Foo {
    pub fn new() -> Self { Self }
    pub fn name(&self) -> &str { "" }
    fn private(&self) {}
}

impl Default for Foo {
    fn default() -> Self { Self }
}
`
	path := writeTempFile(t, "test.rs", src)
	out := mustIndex(t, path)
	assertContains(t, out, "Impl: Foo", "rust impl")
	assertContains(t, out, "new() -> Self", "rust impl method new")
	assertContains(t, out, "name(&self) -> &str", "rust impl method name")
	assertContains(t, out, "private(&self)", "rust impl private method")
	assertContains(t, out, "Impl: Default for Foo", "rust impl trait")
	assertContains(t, out, "default() -> Self", "rust impl trait method")
}

func TestTypeScriptExportVariations(t *testing.T) {
	src := `export function foo() {}
export const X = 1;
export class Bar {}
export interface IBaz {}
export type Alias = string;
export enum Color { Red, Blue }

function internal() {}
`
	path := writeTempFile(t, "test.ts", src)
	out := mustIndex(t, path)
	assertContains(t, out, "Function: foo()", "ts export function")
	assertContains(t, out, "Constant: X", "ts export const")
	assertContains(t, out, "Class: Bar", "ts export class")
	assertContains(t, out, "Trait: IBaz", "ts export interface")
	assertContains(t, out, "Type: Alias", "ts export type")
	assertContains(t, out, "Color enum", "ts export enum")
	assertContains(t, out, "Function: internal()", "ts non-exported function")
}

func TestCPreprocessorConditionals(t *testing.T) {
	src := `#ifdef DEBUG
void debug_log(const char *msg) {}
#endif

#ifndef RELEASE
int dev_mode() { return 1; }
#endif
`
	path := writeTempFile(t, "test.c", src)
	out := mustIndex(t, path)
	assertContains(t, out, "Function: debug_log(const char *msg)", "c function in ifdef")
	assertContains(t, out, "Function: dev_mode()", "c function in ifndef")
}

func TestJavaRecordAndEnum(t *testing.T) {
	src := `package test;

public record Person(String name, int age) {}

enum Direction {
    NORTH, SOUTH, EAST, WEST
}
`
	path := writeTempFile(t, "test.java", src)
	out := mustIndex(t, path)
	assertContains(t, out, "Class: Person", "java record")
	assertContains(t, out, "String name, int age", "java record params")
	assertContains(t, out, "Type: Direction enum", "java enum")
}

func TestRubyModuleMethods(t *testing.T) {
	src := `module Greeter
  def hello
    "hi"
  end

  def self.instance
    Greeter.new
  end
end
`
	path := writeTempFile(t, "test.rb", src)
	out := mustIndex(t, path)
	assertContains(t, out, "Module: Greeter", "ruby module")
	assertContains(t, out, "hello", "ruby module method")
	assertContains(t, out, "self.instance", "ruby module singleton method")
}

func TestBashVariableAssignments(t *testing.T) {
	src := `NAME="test"
COUNT=42
ARRAY=(1 2 3)

function run() {
    echo $NAME
}
`
	path := writeTempFile(t, "test.sh", src)
	out := mustIndex(t, path)
	assertContains(t, out, "Variable: NAME", "bash variable NAME")
	assertContains(t, out, "Variable: COUNT", "bash variable COUNT")
	assertContains(t, out, "Function: run()", "bash function")
}

func TestPythonNoEntriesOnEmpty(t *testing.T) {
	path := writeTempFile(t, "empty.py", "\n")
	out := mustIndex(t, path)
	// Should not error, output can be empty
	if strings.Contains(out, "Function") {
		t.Fatalf("empty file should not contain functions:\n%s", out)
	}
}

func TestLineNumbersPython(t *testing.T) {
	path := writeTempFile(t, "lines.py", samplePython)
	out := mustIndex(t, path)
	// MAX_RETRIES is on line 5
	assertContains(t, out, "Constant: MAX_RETRIES = 3 [5]", "python constant line number")
	// main() starts on line 19
	if !strings.Contains(out, "Function: main() [19") {
		t.Fatalf("expected main() at line 19:\n%s", out)
	}
}

func TestLineNumbersRust(t *testing.T) {
	path := writeTempFile(t, "lines.rs", sampleRust)
	out := mustIndex(t, path)
	// const MAX_SIZE is on line 4
	assertContains(t, out, "Constant: MAX_SIZE: usize = 1024 [4]", "rust const line number")
	// pub fn main() is on line 18
	if !strings.Contains(out, "Function: main() [21") {
		t.Fatalf("expected main() at line 18:\n%s", out)
	}
}

func TestLineNumbersC(t *testing.T) {
	path := writeTempFile(t, "lines.c", sampleC)
	out := mustIndex(t, path)
	// #include <stdio.h> is on line 1
	assertContains(t, out, "Import: #include <stdio.h> [1]", "c include line number")
	// int main is on line 24
	if !strings.Contains(out, "Function: main(int argc, char** argv) [24") {
		t.Fatalf("expected main() at line 24:\n%s", out)
	}
}

func TestLineNumbersJava(t *testing.T) {
	path := writeTempFile(t, "lines.java", sampleJava)
	out := mustIndex(t, path)
	// package is on line 1
	assertContains(t, out, "Package: com.example [1]", "java package line number")
}

// ---- Dockerfile ----

const sampleDockerfile = `FROM golang:1.22 AS builder
ARG VERSION=1.0
ENV GOPATH=/go
WORKDIR /app
COPY . .
RUN go build -o app .
EXPOSE 8080
CMD ["./app"]
`

func TestIndexDockerfile(t *testing.T) {
	path := writeTempFile(t, "Dockerfile", sampleDockerfile)
	out := mustIndex(t, path)

	assertContains(t, out, "Import: FROM golang:1.22 AS builder", "dockerfile from")
	assertContains(t, out, "Variable: ARG VERSION=1.0", "dockerfile arg")
	assertContains(t, out, "Variable: ENV GOPATH=/go", "dockerfile env")
	assertContains(t, out, "Function: RUN go build -o app .", "dockerfile run")
	assertContains(t, out, "Function: CMD", "dockerfile cmd")
	assertContains(t, out, "Function: EXPOSE 8080", "dockerfile expose")
}

func TestIndexDockerfileExt(t *testing.T) {
	path := writeTempFile(t, "dev.dockerfile", sampleDockerfile)
	out := mustIndex(t, path)
	assertContains(t, out, "Import: FROM", "dockerfile by extension")
}

// ---- Go Template ----

const sampleGoTemplate = `{{ define "header" }}
<h1>{{ .Title }}</h1>
{{ end }}

{{ template "header" . }}

{{ block "content" . }}
<p>default</p>
{{ end }}
`

func TestIndexGoTemplate(t *testing.T) {
	path := writeTempFile(t, "page.tmpl", sampleGoTemplate)
	out := mustIndex(t, path)

	assertContains(t, out, "Function: header", "gotemplate define")
	assertContains(t, out, "Import: header", "gotemplate template include")
	assertContains(t, out, "Function: content", "gotemplate block")
}

// ---- Groovy ----

const sampleGroovy = `package com.example

import java.util.List

class Calculator extends Base {
    int add(int a, int b) {
        return a + b
    }

    def multiply(x, y) {
        x * y
    }
}

def greet(name) {
    println "hello ${name}"
}
`

func TestIndexGroovy(t *testing.T) {
	path := writeTempFile(t, "calc.groovy", sampleGroovy)
	out := mustIndex(t, path)

	assertContains(t, out, "Package: com.example", "groovy package")
	assertContains(t, out, "Import: import java.util.List", "groovy import")
	assertContains(t, out, "Class: Calculator extends Base", "groovy class")
	assertContains(t, out, "add", "groovy class method add")
	assertContains(t, out, "multiply", "groovy class method multiply")
	assertContains(t, out, "Function: greet", "groovy top-level function")
}

// ---- Kotlin ----

const sampleKotlin = `package com.example

import kotlin.collections.List

class Person(val name: String, val age: Int) {
    fun greet(): String {
        return "Hi, I am $name"
    }

    val isAdult: Boolean
        get() = age >= 18
}

object Database {
    fun connect(): Connection {
        return DriverManager.getConnection(url)
    }
}

fun add(x: Int, y: Int): Int = x + y

val PI = 3.14159

typealias StringList = List<String>
`

func TestIndexKotlin(t *testing.T) {
	path := writeTempFile(t, "main.kt", sampleKotlin)
	out := mustIndex(t, path)

	assertContains(t, out, "Package: com.example", "kotlin package")
	assertContains(t, out, "Import: import kotlin.collections.List", "kotlin import")
	assertContains(t, out, "Class: Person", "kotlin class")
	assertContains(t, out, "greet", "kotlin class method")
	assertContains(t, out, "isAdult", "kotlin class property")
	assertContains(t, out, "Class: Database", "kotlin object")
	assertContains(t, out, "connect", "kotlin object method")
	assertContains(t, out, "Function: Int add(x, y)", "kotlin top-level function")
	assertContains(t, out, "Variable: PI", "kotlin top-level property")
	assertContains(t, out, "Type: StringList", "kotlin typealias")
}

// ---- Markdown ----

const sampleMarkdown = `# Project Title

Some intro text.

## Installation

Run the following:

    go install

### Prerequisites

- Go 1.22+

## Usage

    wiz --help
`

func TestIndexMarkdown(t *testing.T) {
	path := writeTempFile(t, "README.md", sampleMarkdown)
	out := mustIndex(t, path)

	assertContains(t, out, "Heading: Project Title", "markdown h1")
	assertContains(t, out, "Heading: Installation", "markdown h2")
	assertContains(t, out, "Heading: Prerequisites", "markdown h3")
	assertContains(t, out, "Heading: Usage", "markdown h2 heading")
	assertNotContains(t, out, "Some intro text", "markdown no body text")
}

// ---- Terraform ----

const sampleTerraform = `variable "region" {
  type    = string
  default = "us-east-1"
}

resource "aws_instance" "web" {
  ami           = "ami-12345"
  instance_type = "t3.micro"

  tags = {
    Name = "WebServer"
  }
}

output "instance_id" {
  value = aws_instance.web.id
}
`

func TestIndexTerraform(t *testing.T) {
	path := writeTempFile(t, "main.tf", sampleTerraform)
	out := mustIndex(t, path)

	assertContains(t, out, "Resource: variable region", "terraform variable block")
	assertContains(t, out, "Resource: resource aws_instance web", "terraform resource block")
	assertContains(t, out, "Resource: output instance_id", "terraform output block")
	assertContains(t, out, "ami", "terraform resource attribute")
	assertContains(t, out, "instance_type", "terraform resource attribute")
	assertContains(t, out, "type", "terraform variable attribute")
}

// ---- YAML ----

const sampleYAML = `name: myapp
version: 1.0.0
server:
  port: 8080
  host: localhost
database:
  url: postgres://localhost
  pool: 10
`

func TestIndexYAML(t *testing.T) {
	path := writeTempFile(t, "config.yaml", sampleYAML)
	out := mustIndex(t, path)

	assertContains(t, out, "Variable: name", "yaml top-level key name")
	assertContains(t, out, "Variable: version", "yaml top-level key version")
	assertContains(t, out, "Variable: server", "yaml top-level key server")
	assertContains(t, out, "Variable: database", "yaml top-level key database")
	assertNotContains(t, out, "Variable: port", "yaml no nested key port")
	assertNotContains(t, out, "Variable: host", "yaml no nested key host")
}
