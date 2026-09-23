import { useEffect, useRef } from 'react'
import * as monaco from 'monaco-editor/esm/vs/editor/editor.api.js'
import EditorWorker from 'monaco-editor/esm/vs/editor/editor.worker?worker'
import 'monaco-editor/esm/vs/basic-languages/yaml/yaml.contribution.js'

type MonacoGlobal = typeof globalThis & {
  MonacoEnvironment?: {
    getWorker: () => Worker
  }
}

;(globalThis as MonacoGlobal).MonacoEnvironment = {
  getWorker: () => new EditorWorker(),
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
