# Screenshots

`projects.png`, `dashboard.png`, `resources.png` and `drilldown.png` in the
README are generated,
not captured by hand. The generator lives in
[`internal/ui/screenshot_test.go`](../internal/ui/screenshot_test.go) behind a
`screenshot` build tag, so it never affects a normal build or test run.

They drive the same unexported `Model` the binary does and call the real
`View()`. That is the point: a layout change shows up in the next regenerated
image instead of quietly turning the README into a lie.

Everything shown is invented — the projects, project IDs, service accounts, IPs
and instance names. No real infrastructure appears in any of them.

## Regenerating

Three steps: render the views to ANSI, turn that into SVG, rasterize the SVG.

```sh
go test -tags screenshot -run TestGenerateScreenshots ./internal/ui

go install github.com/charmbracelet/freeze@latest
for n in projects dashboard resources drilldown; do
  freeze --language ansi --output docs/$n.svg \
    --background "#1a1b26" --padding 32 --margin 0 --border.radius 10 \
    --border.width 1 --border.color "#2f3145" --font.size 7 --line-height 1.35 \
    --window docs/$n.ansi
done
```

Then rasterize with the script below. `--output docs/$n.png` would be the
obvious third step, and it is the one that does not work — see the segfault note
under "Things that will bite you".

The intermediate `.ansi` and `.svg` files are build products and are not
committed.

## Things that will bite you

**freeze can hang on first use.** It fetches its embedded webfont, and behind a
proxy that request may never return — the run sits there producing no `.svg` and
no error. Give each invocation a `timeout` rather than letting a loop stall, and
re-run: once the font is cached the whole set renders in seconds.

**freeze drops the last line of its input.** It lays the final line out inside
the bottom padding, where the window border clips it. Reproduce it with a
three-line file — only two come out. The generator appends a spare line of
whitespace to absorb the loss; an *empty* trailing line does not work, because
that gets trimmed before layout.

**freeze's PNG renderer can segfault.** PNG output goes through resvg compiled
to WASM and hosted by wazero, and it died with `SIGSEGV` in `memmove` on both
machines these images have been generated on — reproducibly, on input it had
rendered fine minutes earlier. Render `--output $n.svg` instead (pure Go,
unaffected) and rasterize the SVG. The recipe below is what the current images
were made with; it screenshots the `<svg>` element through Playwright's
Chromium, which avoids most of the traps listed after it:

```js
// node this with playwright available; PLAYWRIGHT_BROWSERS_PATH is already set
// in the dev container.
const { chromium } = require('playwright');
const fs = require('fs'), path = require('path'), os = require('os');

const TARGET_WIDTH = 2344;   // width of the images already in the README
const LINE_HEIGHT = 7 * 1.35; // --font.size x --line-height

(async () => {
  const browser = await chromium.launch();
  for (const name of ['projects', 'dashboard', 'resources', 'drilldown']) {
    const svg = fs.readFileSync(`docs/${name}.svg`, 'utf8');
    const [, w, h] = svg.match(/<svg width="([\d.]+)" height="([\d.]+)"/);

    const page = await browser.newPage({ deviceScaleFactor: TARGET_WIDTH / parseFloat(w) });
    const tmp = path.join(os.tmpdir(), `${name}.html`);
    fs.writeFileSync(tmp, `<!doctype html><html><head><meta charset="utf-8">` +
      `<style>html,body{margin:0;padding:0}svg{display:block}</style></head>` +
      `<body>${svg.replace(/<\?xml[^>]*\?>|<!DOCTYPE[^>]*>/g, '')}</body></html>`);
    await page.goto('file://' + tmp);
    await page.waitForTimeout(400); // let the embedded webfont settle

    // Clip the spare line back off: it has to be in freeze's input and out of
    // the image. See the note above.
    await page.screenshot({
      path: `docs/${name}.png`, omitBackground: true,
      clip: { x: 0, y: 0, width: +w, height: +h - LINE_HEIGHT },
    });
    await page.close();
  }
  await browser.close();
})();
```

Note the clip, and note that the spare line the generator appends is *not*
optional here. Dropping the last line is freeze's layout, not its PNG renderer:
remove the spare line from the `.ansi` and the footer becomes the last line and
vanishes from the SVG too — which is easy to miss, because the image still looks
plausible without it. `grep -c unavailable docs/resources.svg` is the quick
check; that warning lives in the footer of that shot.

Three more things matter if you rasterize some other way:

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

`shotWidth` is shared so every image comes out the same width and they sit flush
when stacked. Heights are per-shot and sized to leave one blank row above the
footer.

Zone names in the resource table are kept to `us-*` on purpose: a real
`northamerica-northeast1-a` is 25 characters, and the proportionally-sized ZONE
column only earns that much space past roughly 195 columns. Accurate, but it
makes for a screenshot full of ellipses.
