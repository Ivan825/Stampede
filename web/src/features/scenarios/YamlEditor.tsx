import { useEffect, useRef } from 'react';
import { useIsDark } from '@/components/misc';
import { defineThemes, monaco, SCENARIO_MODEL_PREFIX } from '@/lib/monaco/setup';

export interface EditorMarker {
  line: number;
  column: number;
  message: string;
  severity: 'error' | 'warning' | 'info';
}

let themesDefined = false;
let modelSeq = 0;

/**
 * The scenario YAML editor. Uncontrolled after mount: `value` seeds the
 * model and replaces it only when `resetKey` changes (e.g. viewing another
 * version), so typing never fights React state.
 */
export function YamlEditor({
  value,
  resetKey,
  readOnly,
  onChange,
  onSave,
  onMarkers,
  revealLine,
}: {
  value: string;
  resetKey: string | number;
  readOnly?: boolean;
  onChange?: (v: string) => void;
  onSave?: () => void;
  onMarkers?: (m: EditorMarker[]) => void;
  revealLine?: { line: number; n: number };
}) {
  const host = useRef<HTMLDivElement>(null);
  const editor = useRef<monaco.editor.IStandaloneCodeEditor | null>(null);
  const cbs = useRef({ onChange, onSave, onMarkers });
  const dark = useIsDark();
  useEffect(() => {
    cbs.current = { onChange, onSave, onMarkers };
  });

  useEffect(() => {
    if (!themesDefined) {
      defineThemes();
      themesDefined = true;
    }
    const uri = monaco.Uri.parse(`${SCENARIO_MODEL_PREFIX}${++modelSeq}.yaml`);
    const model = monaco.editor.createModel('', 'yaml', uri);
    const ed = monaco.editor.create(host.current!, {
      model,
      automaticLayout: true,
      minimap: { enabled: false },
      fontFamily: 'ui-monospace, "SF Mono", Menlo, Consolas, monospace',
      fontSize: 13,
      lineHeight: 20,
      tabSize: 2,
      insertSpaces: true,
      scrollBeyondLastLine: false,
      renderLineHighlight: 'line',
      quickSuggestions: { other: true, strings: true, comments: false },
      padding: { top: 8 },
      fixedOverflowWidgets: true,
      ariaLabel: 'Scenario YAML editor',
    });
    editor.current = ed;
    const sub = model.onDidChangeContent(() => cbs.current.onChange?.(model.getValue()));
    ed.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => cbs.current.onSave?.());
    const markersSub = monaco.editor.onDidChangeMarkers((uris) => {
      if (!uris.some((u) => u.toString() === uri.toString())) return;
      const ms = monaco.editor.getModelMarkers({ resource: uri }).map((m) => ({
        line: m.startLineNumber,
        column: m.startColumn,
        message: m.message,
        severity:
          m.severity === monaco.MarkerSeverity.Error
            ? ('error' as const)
            : m.severity === monaco.MarkerSeverity.Warning
              ? ('warning' as const)
              : ('info' as const),
      }));
      cbs.current.onMarkers?.(ms);
    });
    return () => {
      sub.dispose();
      markersSub.dispose();
      ed.dispose();
      model.dispose();
      editor.current = null;
    };
  }, []);

  // Replace content when the caller switches documents.
  useEffect(() => {
    const ed = editor.current;
    const model = ed?.getModel();
    if (model && model.getValue() !== value) {
      model.setValue(value);
      ed?.setScrollTop(0);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- only on resetKey
  }, [resetKey]);

  useEffect(() => {
    editor.current?.updateOptions({ readOnly: !!readOnly });
  }, [readOnly]);

  useEffect(() => {
    monaco.editor.setTheme(dark ? 'stampede-dark' : 'stampede-light');
  }, [dark]);

  useEffect(() => {
    if (!revealLine || !editor.current) return;
    editor.current.revealLineInCenter(revealLine.line);
    editor.current.setPosition({ lineNumber: revealLine.line, column: 1 });
    editor.current.focus();
  }, [revealLine]);

  return <div ref={host} className="h-full w-full" />;
}
