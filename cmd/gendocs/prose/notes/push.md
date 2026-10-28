If the tag already holds the same content, `pacto push` succeeds and writes nothing. If it holds different content, `pacto push` refuses with a non-zero exit and writes nothing. Use `--force` to overwrite.

!!! note "`--values` has no `-f` shorthand on push"
    On every other override-taking command `-f` is the shorthand for `--values`. On `pacto push`, `-f` is `--force`, so `--values` must be spelled out in full here.
