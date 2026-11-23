import { useEffect, useMemo, useState } from "preact/hooks";
import type { HighlighterCore } from "shiki/core";
import { CodePane } from "./components/CodePane.tsx";
import { createBaitHighlighter, highlightCode } from "./highlight.ts";
import { translateWithWasm } from "./wasm.ts";

const INITIAL_BASH = `#!/usr/bin/env bash
quote="a tropical fish yearns for snow"

echo "Length:    \${#quote} characters"
echo "Replaced:  \${quote/tropical fish/penguin}"
echo "Uppercase: $(echo "$quote" | tr '[:lower:]' '[:upper:]')"

read -ra words <<< "$quote"

for word in "\${words[@]}"; do
    if [[ "$word" == "snow" ]]; then
        echo "Found keyword: '$word'"
    fi
done

summarize() {
    echo "Total words processed: $#";
}
summarize "\${words[@]}"`;

export function App() {
  const [bashCode, setBashCode] = useState(INITIAL_BASH);
  const [fishCode, setFishCode] = useState("");
  const [stderr, setStderr] = useState("");
  const [wasmModule, setWasmModule] = useState<WebAssembly.Module | null>(null);
  const [highlighter, setHighlighter] = useState<HighlighterCore | null>(null);

  useEffect(() => {
    WebAssembly.compileStreaming(fetch("./bait.wasm"))
      .then(setWasmModule)
      .catch((err) => setStderr(String(err)));

    createBaitHighlighter()
      .then(setHighlighter)
      .catch((err) => setStderr(String(err)));
  }, []);

  useEffect(() => {
    if (!wasmModule) return;
    let cancelled = false;

    translateWithWasm(wasmModule, bashCode)
      .then((res) => {
        if (cancelled) return;
        setFishCode(res.stdout);
        setStderr(res.stderr);
      })
      .catch((err) => {
        if (cancelled) return;
        setStderr(String(err));
      });

    return () => {
      cancelled = true;
    };
  }, [wasmModule, bashCode]);

  const bashHtml = useMemo(
    () => highlightCode(highlighter, bashCode, "bash"),
    [highlighter, bashCode],
  );
  const fishHtml = useMemo(
    () => highlightCode(highlighter, fishCode, "fish"),
    [highlighter, fishCode],
  );

  return (
    <>
      <header class="flex items-center justify-between px-6 py-4 border-b border-base-content/20 shrink-0">
        <span class="text-xl font-semibold tracking-tight">Bait</span>
        <a
          href="https://github.com/kidonng/bait"
          target="_blank"
          rel="noopener noreferrer"
          class="text-base hover:underline"
        >
          GitHub
        </a>
      </header>

      <main class="flex-1 min-h-0 flex flex-col gap-4 p-4">
        <div class="flex-1 min-h-0 grid grid-cols-1 md:grid-cols-2 gap-4">
          <CodePane
            label="bash in"
            code={bashCode}
            highlightedHtml={bashHtml}
            onInput={setBashCode}
          />
          <CodePane
            label="fish out"
            code={fishCode}
            highlightedHtml={fishHtml}
            readOnly
          />
        </div>

        <section
          aria-label="stderr"
          class="h-28 shrink-0 rounded-2xl border border-base-content/20 bg-base-200/40 overflow-auto p-4 font-mono text-sm whitespace-pre-wrap"
        >
          {stderr ? (
            <span class="text-warning">{stderr}</span>
          ) : (
            <span class="text-base-content/40 select-none">stderr</span>
          )}
        </section>
      </main>
    </>
  );
}
