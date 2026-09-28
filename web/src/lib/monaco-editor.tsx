import { useEffect, useRef } from 'react'
import * as monaco from 'monaco-editor/esm/vs/editor/editor.api.js'
import EditorWorker from 'monaco-editor/esm/vs/editor/editor.worker?worker'
import 'monaco-editor/esm/vs/basic-languages/yaml/yaml.contribution.js'
import 'monaco-editor/esm/vs/basic-languages/go/go.contribution.js'
import 'monaco-editor/esm/vs/basic-languages/shell/shell.contribution.js'
import 'monaco-editor/esm/vs/basic-languages/markdown/markdown.contribution.js'
import 'monaco-editor/esm/vs/basic-languages/ini/ini.contribution.js'
import 'monaco-editor/esm/vs/basic-languages/dockerfile/dockerfile.contribution.js'
import 'monaco-editor/esm/vs/basic-languages/xml/xml.contribution.js'
import 'monaco-editor/esm/vs/basic-languages/python/python.contribution.js'
import 'monaco-editor/esm/vs/basic-languages/sql/sql.contribution.js'
import 'monaco-editor/esm/vs/language/typescript/monaco.contribution.js'
import 'monaco-editor/esm/vs/language/json/monaco.contribution.js'
import 'monaco-editor/esm/vs/language/css/monaco.contribution.js'
import 'monaco-editor/esm/vs/language/html/monaco.contribution.js'
import TypeScriptWorker from 'monaco-editor/esm/vs/language/typescript/ts.worker?worker'
import JSONWorker from 'monaco-editor/esm/vs/language/json/json.worker?worker'
import CSSWorker from 'monaco-editor/esm/vs/language/css/css.worker?worker'
import HTMLWorker from 'monaco-editor/esm/vs/language/html/html.worker?worker'

type MonacoGlobal = typeof globalThis & {
  MonacoEnvironment?: {
    getWorker: (_moduleId: string, label: string) => Worker
  }
}

;(globalThis as MonacoGlobal).MonacoEnvironment = {
  getWorker: (_moduleId, label) => {
    if (label === 'typescript' || label === 'javascript') return new TypeScriptWorker()
    if (label === 'json') return new JSONWorker()
    if (label === 'css' || label === 'scss' || label === 'less') return new CSSWorker()
    if (label === 'html' || label === 'handlebars' || label === 'razor') return new HTMLWorker()
    return new EditorWorker()
  },
}

interface MonacoEditorProps {
  language: string
  theme: string
  value: string
  onChange?: (value: string | undefined) => void
  options?: monaco.editor.IStandaloneEditorConstructionOptions
}

export default function MonacoEditor({ language, theme, value, onChange, options }: MonacoEditorProps) {
  const containerRef = useRef<HTMLDivElement>(null)
  const editorRef = useRef<monaco.editor.IStandaloneCodeEditor | null>(null)
  const onChangeRef = useRef(onChange)
  const initialRef = useRef({ language, value, options, theme })
  onChangeRef.current = onChange

  useEffect(() => {
    if (!containerRef.current) return
    const initial = initialRef.current
    const model = monaco.editor.createModel(initial.value, initial.language)
    const editor = monaco.editor.create(containerRef.current, { ...initial.options, model, theme: initial.theme })
    editorRef.current = editor
    const subscription = editor.onDidChangeModelContent(() => onChangeRef.current?.(editor.getValue()))
    return () => {
      subscription.dispose()
      editor.dispose()
      model.dispose()
      editorRef.current = null
    }
  }, [])

  useEffect(() => {
    monaco.editor.setTheme(theme)
  }, [theme])

  useEffect(() => {
    const editor = editorRef.current
    if (editor && editor.getValue() !== value) editor.setValue(value)
  }, [value])

  useEffect(() => {
    const model = editorRef.current?.getModel()
    if (model && model.getLanguageId() !== language) monaco.editor.setModelLanguage(model, language)
  }, [language])

  useEffect(() => {
    editorRef.current?.updateOptions(options ?? {})
  }, [options])

  return <div ref={containerRef} className="h-full w-full" />
}

export function MonacoDiffEditor({ original, modified, language, theme }: { original: string; modified: string; language: string; theme: string }) {
  const element = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!element.current) return
    const before = monaco.editor.createModel(original, language)
    const after = monaco.editor.createModel(modified, language)
    const editor = monaco.editor.createDiffEditor(element.current, { theme, readOnly: true, automaticLayout: true, minimap: { enabled: false }, renderSideBySide: true })
    editor.setModel({ original: before, modified: after })
    return () => { editor.dispose(); before.dispose(); after.dispose() }
  }, [original, modified, language, theme])
  return <div ref={element} className="h-full w-full" />
}
