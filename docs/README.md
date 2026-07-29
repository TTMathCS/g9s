# Screenshots

`projects.png`, `dashboard.png` and `resources.png` in the README are generated,
not captured by hand. The generator lives in
[`internal/ui/screenshot_test.go`](../internal/ui/screenshot_test.go) behind a
`screenshot` build tag, so it never affects a normal build or test run.

It drives the same unexported `Model` the binary does and calls the real
`View()`. That is the point: a layout change shows up in the next regenerated
image instead of quietly turning the README into a lie.

Everything shown is invented — the projects, project IDs, service accounts, IPs
and instance names. No real infrastructure appears in either image.

## Regenerating

Two steps. The first renders the views to ANSI, the second turns that into a PNG:

```sh
go test -tags screenshot -run TestGenerateScreenshots ./internal/ui

go install github.com/charmbracelet/freeze@latest
for n in projects dashboard resources; do
  freeze --language ansi --output docs/$n.png \
    --background "#1a1b26" --padding 32 --margin 0 --border.radius 10 \
    --border.width 1 --border.color "#2f3145" --font.size 7 --line-height 1.35 \
    --window docs/$n.ansi
done
```

The intermediate `.ansi` and `.svg` files are build products and are not
committed.

## Things that will bite you

**freeze drops the last line of its input.** It lays the final line out inside
the bottom padding, where the window border clips it. Reproduce it with a
three-line file — only two come out. The generator appends a spare line of
whitespace to absorb the loss; an *empty* trailing line does not work, because
that gets trimmed before layout.

**freeze's PNG renderer can segfault.** PNG output goes through resvg compiled
to WASM and hosted by wazero, and it died with `SIGSEGV` in `memmove` on the
machine these images were last generated on — reproducibly, on input it had
rendered fine minutes earlier. If that happens, render `--output $n.svg`
instead (pure Go, unaffected) and rasterize the SVG yourself. Three things
matter if you go that route:

- **Chromium's `--window-size` counts browser chrome.** The content area comes
  out 87 css px shorter than requested while the screenshot canvas stays
  window-sized, so the capture ends up with a dead strip at the bottom and the
  content squeezed into ~79% of it. Both headless modes behave identically.
  Request `height + 87` and crop the extra rows back off. Verify with a
  solid-colour probe rather than trusting the numbers — a plain `415px` red div
  makes the offset obvious in one screenshot.
- **Declare the charset.** Inline the SVG into an HTML page without
  `<meta charset="utf-8">` and Chromium reads a `file://` document as
  windows-1252, turning every `·`, `▸` and `⚠` into mojibake. The standalone
  SVG carries its own XML encoding, so this only appears once the markup is
  inlined.
- **Don't load the SVG through `<img>`.** It renders at ~79% height there for
  the same window-size reason, and adding a `viewBox` does not help.

## Geometry

`shotWidth` is shared so both images come out the same width and sit flush when
stacked. Heights are per-shot and sized to leave one blank row above the footer.

Zone names in the resource table are kept to `us-*` on purpose: a real
`northamerica-northeast1-a` is 25 characters, and the proportionally-sized ZONE
column only earns that much space past roughly 195 columns. Accurate, but it
makes for a screenshot full of ellipses.
