---
"@pacto/core": minor
---

`pacto diff` judges every interface schema change by the position the schema
sits in.

**`pacto diff` and `pacto impact` report more changes as `BREAKING`.** A CI
job that gates on `BREAKING` can fail on a change it passed before. Each of
these was `POTENTIAL_BREAKING`:

- In a response or an MCP `outputSchema`, widening breaks: a new `enum` value
  or `oneOf` variant, a looser limit, a dropped `pattern`, a wider `type`.
- In a request body, a parameter or an MCP `inputSchema`, narrowing breaks: a
  dropped `enum` value, a tighter limit, a new `pattern`.
- An AsyncAPI channel or operation is a record that old and new code read and
  write at once, so widening and narrowing both break. Only a new optional
  property, a new 2.x operation and documentation pass. Removing a payload
  property is `BREAKING`, as it already was from a response.
- Inside an interface schema, an edit the rules cannot rank, such as a new
  `default`, a repointed `$ref` or anything under `not`, is `BREAKING`.
- A proto `enum` is compared for the first time: a removed enum and an added,
  removed or renumbered value are `BREAKING`.

Some changes grade lower than before. The safe direction of each edit above is
`NON_BREAKING`, and so is a new optional property in a request or a response.
Documentation edits (`description`, `title`, `summary`, `example`, `examples`,
`$comment`, `externalDocs`, `deprecated`) are `NON_BREAKING` everywhere,
configuration and policy schemas included.
