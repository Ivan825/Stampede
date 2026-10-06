/**
 * Monaco with YAML only, bundled locally (no CDN), with monaco-yaml bound to
 * the scenario JSON Schema for validation, completion and hover docs.
 */
import * as monaco from 'monaco-editor/esm/vs/editor/editor.api';
import './contributions';
import { configureMonacoYaml } from 'monaco-yaml';
import EditorWorker from './editor.worker.ts?worker';
import YamlWorker from './yaml.worker.ts?worker';
import scenarioSchema from '../../../../schema/scenario.schema.json';

declare global {
  interface Window {
    MonacoEnvironment?: { getWorker(workerId: string, label: string): Worker };
  }
}

window.MonacoEnvironment = {
  getWorker(_id, label) {
    return label === 'yaml' ? new YamlWorker() : new EditorWorker();
  },
};

// monaco-editor's full entry (editor.main) installs this shim so language
// workers can be created from { moduleId, label, createData }; we load the
// slimmer core entry, so install the same shim for monaco-yaml.
type WorkerOpts = Parameters<typeof monaco.editor.createWebWorker>[0] & {
  worker?: Worker | Promise<Worker>;
};
const coreCreateWebWorker = monaco.editor.createWebWorker as (o: unknown) => unknown;
(monaco.editor as { createWebWorker: unknown }).createWebWorker = (opts: WorkerOpts) => {
  if (opts.worker !== undefined) return coreCreateWebWorker(opts);
  const worker = Promise.resolve(
    window.MonacoEnvironment!.getWorker('workerMain.js', opts.label ?? 'monaco-editor-worker'),
  ).then((w) => {
    w.postMessage('ignore');
    w.postMessage(opts.createData as unknown);
    return w;
  });
  const host: unknown = opts.host;
  return coreCreateWebWorker({ worker, host, keepIdleModels: opts.keepIdleModels });
};

export const SCENARIO_MODEL_PREFIX = 'inmemory://stampede/scenario/';

configureMonacoYaml(monaco, {
  enableSchemaRequest: false,
  hover: true,
  completion: true,
  validate: true,
  schemas: [
    {
      uri: 'https://github.com/Ivan825/Stampede/schema/scenario.schema.json',
      fileMatch: [`${SCENARIO_MODEL_PREFIX}*`],
      // The schema is draft 2020-12; monaco-yaml understands the subset used.
      schema: scenarioSchema,
    },
  ],
});

/** Defines editor themes from the current CSS tokens. */
export function defineThemes() {
  const light = {
    bg: '#ffffff',
    line: '#eef0f2',
    fg: '#12161a',
    muted: '#5b6670',
  };
  monaco.editor.defineTheme('stampede-light', {
    base: 'vs',
    inherit: true,
    rules: [
      { token: 'type', foreground: '9a3412' },
      { token: 'string', foreground: '17602f' },
      { token: 'number', foreground: '2463c4' },
      { token: 'comment', foreground: '6b7680', fontStyle: 'italic' },
    ],
    colors: {
      'editor.background': light.bg,
      'editor.lineHighlightBackground': '#f6f7f8',
      'editorLineNumber.foreground': '#9aa4ad',
      'editorLineNumber.activeForeground': light.fg,
      'editorGutter.background': light.bg,
      'editorIndentGuide.background1': light.line,
    },
  });
  monaco.editor.defineTheme('stampede-dark', {
    base: 'vs-dark',
    inherit: true,
    rules: [
      { token: 'type', foreground: 'ff9a5c' },
      { token: 'string', foreground: '7fd6a4' },
      { token: 'number', foreground: '8ab8ff' },
      { token: 'comment', foreground: '7d8995', fontStyle: 'italic' },
    ],
    colors: {
      'editor.background': '#161b20',
      'editor.lineHighlightBackground': '#1c2228',
      'editorLineNumber.foreground': '#56616c',
      'editorLineNumber.activeForeground': '#e8ecef',
      'editorGutter.background': '#161b20',
      'editorIndentGuide.background1': '#262e36',
    },
  });
}

export { monaco };
