;; Generic function call patterns for Tier B languages.

;; Direct function calls: foo(args)
(call_expression function: (identifier) @call.target) @call.site

;; Method calls: obj.method(args)
(call_expression
  function: (member_expression
    property: (property_identifier) @call.target)) @call.site

;; Attribute calls: obj.method(args) — Python/Ruby style
(call
  function: (attribute
    attribute: (identifier) @call.target)) @call.site

;; Simple calls — Ruby/Elixir
(call
  method: (identifier) @call.target) @call.site

;; Scoped calls: Module::function() — Rust/C++
(call_expression
  function: (scoped_identifier
    name: (identifier) @call.target)) @call.site
