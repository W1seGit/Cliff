"use client";

import { useMemo, useRef } from "react";
import { Download } from "lucide-react";
import { serverPropertiesUrl } from "../../lib/runtime-client";
import { splitLines, type PropertiesIssue } from "../../lib/properties-text";
import { Banner, Button, Card, CopyButton } from "../../components/ui";

const LINE_HEIGHT = 20;

type Token = { text: string; className?: string };

function tokenizeLine(line: string): Token[] {
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

/**
 * A small code editor for server.properties: line numbers, colour for keys and
 * values, and live problem markers. The textarea is transparent and sits on top
 * of a highlighted copy of the text, so typing, selection and undo are native.
 */
function PropertiesEditor({
  value,
  onChange,
  issues,
  disabled,
}: {
  value: string;
  onChange: (text: string) => void;
  issues: PropertiesIssue[];
  disabled?: boolean;
}) {
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
      <div className={`props-editor ${issues.length > 0 ? "has-issues" : ""}`}>
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
                {tokenizeLine(line).map((token, tokenIndex) => (
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
            aria-label="server.properties"
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

export function PropertiesEditorCard({
  serverId,
  value,
  onChange,
  issues,
  running,
}: {
  serverId: string;
  value: string;
  onChange: (text: string) => void;
  issues: PropertiesIssue[];
  running: boolean;
}) {
  const propertyCount = useMemo(
    () => splitLines(value).filter((line) => {
      const trimmed = line.trim();
      return trimmed !== "" && !trimmed.startsWith("#") && !trimmed.startsWith("!") && line.includes("=");
    }).length,
    [value],
  );
  return (
    <Card
      title="server.properties"
      description={`The real file, ${propertyCount} ${propertyCount === 1 ? "property" : "properties"}. The fields on the Game tab edit the same text, and your comments are kept.`}
      actions={
        <>
          <CopyButton text={value} label="Copy server.properties" />
          <Button variant="link" iconLeft={<Download size={14} />} href={serverPropertiesUrl(serverId, "?download=1")} download>
            Download
          </Button>
        </>
      }
    >
      {running && <Banner variant="warning">The server is running. Changes to this file take effect after a restart.</Banner>}
      <PropertiesEditor value={value} onChange={onChange} issues={issues} />
    </Card>
  );
}
