import { createHighlighterCore, type HighlighterCore } from "shiki/core";
import { createJavaScriptRegexEngine } from "shiki/engine/javascript";
import bash from "shiki/langs/bash.mjs";
import fish from "shiki/langs/fish.mjs";
import githubDark from "shiki/themes/github-dark.mjs";
import githubLight from "shiki/themes/github-light.mjs";

export type SupportedLang = "bash" | "fish";

/**
 * Creates a minimal Shiki highlighter bundled with only Bash/Fish grammars and GitHub light/dark themes.
 */
export function createBaitHighlighter(): Promise<HighlighterCore> {
  return createHighlighterCore({
    themes: [githubLight, githubDark],
    langs: [bash, fish],
    engine: createJavaScriptRegexEngine(),
  });
}

/**
 * Highlights source code with dual light/dark CSS variables for `light-dark()`.
 */
export function highlightCode(
  highlighter: HighlighterCore | null,
  code: string,
  lang: SupportedLang,
): string {
  if (!highlighter || !code) return "";
  const normalized = code.endsWith("\n") ? `${code} ` : code;
  return highlighter.codeToHtml(normalized, {
    lang,
    themes: {
      light: "github-light",
      dark: "github-dark",
    },
    defaultColor: false,
  });
}
