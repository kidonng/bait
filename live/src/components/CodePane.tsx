export interface CodePaneProps {
  label: string;
  code: string;
  highlightedHtml: string;
  readOnly?: boolean;
  onInput?: (value: string) => void;
}

/**
 * Renders a syntax-highlighted code overlay backed by a synchronized textarea.
 */
export function CodePane({
  label,
  code,
  highlightedHtml,
  readOnly = false,
  onInput,
}: CodePaneProps) {
  const fallbackCode = code.endsWith("\n") ? `${code} ` : code;

  return (
    <section
      aria-label={label}
      class="rounded-2xl border border-base-content/20 bg-base-200/40 overflow-auto relative"
    >
      <div class="code-grid">
        {highlightedHtml ? (
          <div dangerouslySetInnerHTML={{ __html: highlightedHtml }} />
        ) : (
          <pre>
            <code>{fallbackCode}</code>
          </pre>
        )}
        <textarea
          aria-label={label}
          placeholder={label}
          readOnly={readOnly}
          spellcheck={false}
          autocapitalize="off"
          autocomplete="off"
          autocorrect="off"
          value={code}
          onInput={onInput ? (e) => onInput(e.currentTarget.value) : undefined}
        />
      </div>
    </section>
  );
}
