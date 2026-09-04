---
"@pacto/core": patch
---

Make the dashboard move, and make what it draws honest.

Motion is a closed system rather than a per-component decision. Four roles —
feedback, flip, reveal, dismiss — are declared once in `tokens.css`, and a
ration governs who may use them: only an error state may enter on its own and
carry the alarm ring, a warning may enter but never rings, and every other tone
stays still. The number of moving things on a screen is the number of things
wrong with it. Every entrance respects `prefers-reduced-motion`.

The graph stops being a picture and becomes an instrument. The subject of the
page carries a standing halo, so arriving on a graph tells you what it is about;
a click pins a directional spotlight and re-frames the camera without ever
re-laying-out; edges answer the pointer; a fit can no longer zoom past the point
where labels are text. The legend was a caption listing distinctions the canvas
actually draws — every entry is now a toggle that dims exactly that distinction,
because a dense neighborhood is read by taking things out of it. Picking from
the accessible text list points the canvas at the same node, so the two halves
of the screen can no longer describe different things, and a summary line states
the counts, what the legend is hiding and what is selected.

Three bespoke charts are gone. A treemap, a donut and a second bar chart are
replaced by the two house forms already used elsewhere, and
`cytoscape-expand-collapse` — a dependency behind a toolbar nothing rendered —
is removed with it.

Chart corrections, each a case of the drawing contradicting the data:

- The priority quadrant measured every service against a fixed midpoint while
  the gate is per service, so a service scoring 60 against a threshold of 80 sat
  on the healthy side of the line while failing. Points are now plotted as
  distance from their own threshold, and the divider says what it is.
- Dot radius encoded blast radius, which is already the y position — the same
  number drawn twice. One radius for every dot.
- The version timeline positioned markers by date and drew no date axis, so the
  only way to read one was to hover it.
- Compliance status was three separate tables — the row badge, the legend swatch
  and the graph node — and they disagreed. "Unknown" was a blue badge, an amber
  swatch and a grey node on one screen. One table now decides both the wording
  and the tone, and every surface reads it.
- Distribution shares printed a decimal below a population of a hundred, stating
  "12.5%" where the smallest step the data can take is 12.5 points. The same
  rounding ran out at the other end: one invalid target in a fleet of three
  thousand printed as "1 (0% of 3000)", a row contradicting the count beside it,
  and it is exactly the row a triage page exists to surface. A non-zero share now
  prints as the bound it is under rather than as nought.

A drawer opening beside the graph pushed the page off the right of the screen.
Cytoscape writes its current pixel width onto a wrapper inside the canvas, so the
graph's minimum width was whatever it was last laid out at, and no track holding
it could shrink. The graph now contains its own inline axis and sizes from the
outside in, which fixes every layout that embeds it rather than the one that
happened to notice. A claim's source revision is abbreviated rather than printed
as a full digest, and the responsive gate reads the body's scroll width as well
as the document's — clipped overflow is invisible to the document — at desktop
widths as well, where the drawer that started this actually opens.

Change analysis joins the long pages on the shared "On this page" rail. A finished
analysis runs several screens deep — the revision pickers, the change table, then
the consumer table under it — and getting back up to compare a different pair was a
scroll. It is the navigator the overview and the entity pages already use, so there
is no second contents list, and it lists only what the page actually rendered: there
is no "What it affects" entry until there is a result.

The overview draws populations it can count. Nine operational targets on a
proportional bar is a shape the reader has to convert back into nine things;
drawn as nine marks, two of them red, the count is the picture. Past a hundred
and twenty members the marks stop being countable and the proportion is the
honest reading again, so the bar comes back — and a population that over-counts
itself is never drawn as marks at all, because it has no individuals to draw. The
prose around them is cut: a sentence under the posture bars restated two bucket
values that were already on the screen, so there are no longer two copies of the
same number to check against each other.

Those marks are now sized from the population, so a small one is big: nine
targets drawn as nine fixed sixteen-pixel squares was a smudge in a
four-hundred-pixel column — countable in principle and nothing to look at — and
the size steps down only as fast as it has to for the row to keep fitting. The
gutter and the corner are fractions of the mark, so a field of nine and a field
of ninety are the same drawing at two scales rather than two different charts.
The three posture questions sit in three columns where there is room, instead of
two and an orphan below an empty half-row. And every distribution now puts its
picture first: the description used to sit between the heading and the graphic,
so a band of three charts was read as six lines of caveat with drawings between
them, and each figure announced itself to a screen reader by reciting the whole
paragraph. The prose is a footnote to the drawing, so it is printed under it.

The WebAssembly demo's notice can be dismissed. It floats over the bottom of the
dashboard and never left, which on a short window is where the content is. A
failed engine load brings it back: that is the one message the reader cannot be
allowed to have closed.

Ten scale limits are closed. Measured against a fleet of five hundred services,
two thousand revisions, three thousand targets and eight thousand relationships,
each of these was a place where the cost of an answer grew with the size of the
fleet rather than with the size of the question:

- Every concurrent request that missed a cold service index ran its own full
  serial resolution of the whole fleet. One rebuild is admitted at a time and the
  callers queued behind it return the index it stored, doing no work at all.
- A Fleet host issued a whole-fleet `/api/services` it never reads, every two
  seconds. The capability probe used to race the load it decides the shape of, so
  the first pass always read "capabilities unknown", took the legacy branch and
  paid for an answer it threw away. It is probed first now, at the cost of one
  round trip on first load. The poll also no longer stacks a second pass on top
  of one still in flight, while a manual refresh is still never dropped.
- The whole-fleet dependency graph had no node bound. It takes one, defaulting to
  the engine's ceiling, and every node carries the path taken to reach it — so an
  unbounded deep answer was quadratic on the wire, not linear.
- `Meta` applied none of the envelope caps `ProductMeta` applies, so two answers
  from the same snapshot could disagree about how much they left out.
- The per-target revision match rebuilt its candidate set from every revision in
  the fleet; it is grouped once per service and reused.
- The bulk snapshot export round-tripped through a marshal-and-unmarshal
  defensive copy on its way to a wire it is then written to and dropped.
- A truncated graph said only that it had been truncated. It now says how much is
  missing, and offers the next node budget — sixty, a hundred and fifty, five
  hundred, the same rungs the backend will honour, because a control that offers a
  step the server then clamps is a control that lies about what it just fetched.
  The budget is part of the URL, so a shared link reopens the graph being
  discussed rather than a smaller one.
- A fit clamped at the legibility floor left part of the graph off screen and
  said nothing, and a cropped canvas looks exactly like a complete one. It says so.
