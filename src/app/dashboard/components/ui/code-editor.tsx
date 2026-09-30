"use client";

import { useMemo, useRef } from "react";

const LINE_HEIGHT = 20;

export type CodeLanguage = "properties" | "plain";
export type CodeIssue = { line: number; message: string };

type Token = { text: string; className?: string };

function splitLines(text: string): string[] {
  return text.split("\n").map((line) => line.replace(/\r$/, ""));
}

function tokenizeProperties(line: string): Token[] {
  const trimmed = line.trim();
  if (trimmed === "") return [{ text: line }];
  if (trimmed.startsWith("#") || trimmed.startsWith("!")) return [{ text: line, className: "tok-comment" }];
  const at = line.indexOf("=");
  if (at < 0) return [{ text: line, className: "tok-error" }];
  const value = line.slice(at + 1);
  const bare = value.trim();
  let valueClass = "tok-string";
  if (bare === "true") valueClass = "tok-true";
  else if (bare === "false") valueClass = "tok-false";
  else if (bare !== "" && /^-?\d+(\.\d+)?$/.test(bare)) valueClass = "tok-number";
  return [
    { text: line.slice(0, at), className: "tok-key" },
    { text: "=", className: "tok-eq" },
    { text: value, className: valueClass },
  ];
}

function tokenizeLine(line: string, language: CodeLanguage): Token[] {
  if (language === "properties") return tokenizeProperties(line);
  return [{ text: line }];
}

/** Pick a highlighter from a file name: key=value style files get colour, the rest stay plain. */
export function languageForFile(name: string): CodeLanguage {
  return /\.(properties|cfg|conf|ini|env|toml)$/i.test(name) ? "properties" : "plain";
}

export interface CodeEditorProps {
  value: string;
  onChange: (text: string) => void;
  language?: CodeLanguage;
  issues?: CodeIssue[];
  disabled?: boolean;
  ariaLabel: string;
  /** Fill most of the viewport instead of a fixed medium height. */
  tall?: boolean;
}

/**
 * A small code editor: line numbers, syntax colour and live problem markers.
 * The textarea is transparent and sits on top of a highlighted copy of the
 * text, so typing, selection and undo are all native.
 */
export function CodeEditor({ value, onChange, language = "plain", issues = [], disabled, ariaLabel, tall }: CodeEditorProps) {
  const inputRef = useRef<HTMLTextAreaElement | null>(null);
  const highlightRef = useRef<HTMLPreElement | null>(null);
  const gutterRef = useRef<HTMLDivElement | null>(null);
  const lines = useMemo(() => splitLines(value), [value]);
  const issueLines = useMemo(() => new Set(issues.map((issue) => issue.line)), [issues]);

  function syncScroll() {
    const input = inputRef.current;
    if (!input) return;
    if (highlightRef.current) {
      highlightRef.current.scrollTop = input.scrollTop;
      highlightRef.current.scrollLeft = input.scrollLeft;
    }
    if (gutterRef.current) gutterRef.current.scrollTop = input.scrollTop;
  }

  function jumpToLine(line: number) {
    const input = inputRef.current;
    if (!input) return;
    const all = splitLines(value);
    let offset = 0;
    for (let index = 0; index < line - 1 && index < all.length; index++) offset += all[index].length + 1;
    input.focus();
    input.setSelectionRange(offset, offset + (all[line - 1]?.length ?? 0));
    input.scrollTop = Math.max(0, (line - 3) * LINE_HEIGHT);
    syncScroll();
  }

  return (
    <div className="props-editor-wrap">
      <div className={`props-editor ${tall ? "tall" : ""} ${issues.length > 0 ? "has-issues" : ""}`.replace(/\s+/g, " ").trim()}>
        <div className="props-editor-gutter" ref={gutterRef} aria-hidden="true">
          {lines.map((_, index) => (
            <div key={index} className={issueLines.has(index + 1) ? "gutter-line has-issue" : "gutter-line"}>
              {index + 1}
            </div>
          ))}
        </div>
        <div className="props-editor-body">
          <pre className="props-editor-highlight" ref={highlightRef} aria-hidden="true">
            {lines.map((line, index) => (
              <div key={index} className={issueLines.has(index + 1) ? "code-line has-issue" : "code-line"}>
                {tokenizeLine(line, language).map((token, tokenIndex) => (
                  <span key={tokenIndex} className={token.className}>{token.text}</span>
                ))}
                {"​"}
              </div>
            ))}
          </pre>
          <textarea
            ref={inputRef}
            className="props-editor-input"
            value={value}
            onChange={(event) => onChange(event.target.value)}
            onScroll={syncScroll}
            spellCheck={false}
            autoCapitalize="off"
            autoComplete="off"
            autoCorrect="off"
            wrap="off"
            aria-label={ariaLabel}
            aria-invalid={issues.length > 0 ? true : undefined}
            disabled={disabled}
          />
        </div>
      </div>
      {issues.length > 0 && (
        <ul className="props-editor-issues" role="alert">
          {issues.slice(0, 6).map((issue) => (
            <li key={`${issue.line}:${issue.message}`}>
              <button type="button" onClick={() => jumpToLine(issue.line)}>
                Line {issue.line}
              </button>
              <span>{issue.message}</span>
            </li>
          ))}
          {issues.length > 6 && <li className="muted">and {issues.length - 6} more</li>}
        </ul>
      )}
    </div>
  );
}
