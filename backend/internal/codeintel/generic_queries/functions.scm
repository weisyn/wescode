;; Generic function/method definition patterns for Tier B languages.
;; These S-expression patterns cover the most common tree-sitter node types
;; across 50+ languages. Each grammar has slightly different naming, so we
;; use a fallback chain — the first match wins.

;; Function definitions (covers: Python, JS, Rust, Elixir, Lua, Dart, etc.)
(function_definition name: (identifier) @func.name) @func.def
(function_declaration name: (identifier) @func.name) @func.def
(function_item name: (identifier) @func.name) @func.def

;; Method definitions (covers: Python, Ruby, JS class methods, etc.)
(method_definition name: (identifier) @func.name) @func.def
(method_definition name: (property_identifier) @func.name) @func.def
(method_declaration name: (identifier) @func.name) @func.def

;; Lambda / arrow functions with explicit names (let/const binding)
(lexical_declaration
  (variable_declarator
    name: (identifier) @func.name
    value: (arrow_function))) @func.def

;; Class-level function definitions
(class_body
  (method_definition
    name: (property_identifier) @func.name)) @func.def
