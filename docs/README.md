# Screenshots

`projects.png` and `resources.png` in the README are generated, not captured by
hand. The generator lives in [`internal/ui/screenshot_test.go`](../internal/ui/screenshot_test.go)
behind a `screenshot` build tag, so it never affects a normal build or test run.

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
for n in projects resources; do
  freeze --language ansi --output docs/$n.png \
    --background "#1a1b26" --padding 32 --margin 0 --border.radius 10 \
    --border.width 1 --border.color "#2f3145" --font.size 7 --line-height 1.35 \
    --window docs/$n.ansi
done
```

The intermediate `.ansi` files are build products and are not committed.

## Two things that will bite you

**freeze drops the last line of its input.** It lays the final line out inside
the bottom padding, where the window border clips it. Reproduce it with a
three-line file — only two come out. The generator appends a spare line of
whitespace to absorb the loss; an *empty* trailing line does not work, because
that gets trimmed before layout.

**Don't rasterize the SVG with a browser.** `freeze --output x.svg` produces a
valid SVG, but Chromium renders it into an `<img>` at about 79% of its declared
height, silently cutting off the bottom. Adding a `viewBox` does not fix it.
Use freeze's own PNG output, which is what the command above does.

## Geometry

`shotWidth` is shared so both images come out the same width and sit flush when
stacked. Heights are per-shot and sized to leave one blank row above the footer.

Zone names in the resource table are kept to `us-*` on purpose: a real
`northamerica-northeast1-a` is 25 characters, and the proportionally-sized ZONE
column only earns that much space past roughly 195 columns. Accurate, but it
makes for a screenshot full of ellipses.
