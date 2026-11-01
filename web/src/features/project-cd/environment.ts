import {useBlocker} from '@tanstack/react-router'
import {registerProjectNavigationGuard} from '../../lib/project-navigation-guard'
import {confirmDialog} from '../../stores/dialog'
import {useCallback, useEffect, useRef, useState} from 'react'
import {useMutation, useQueryClient} from '@tanstack/react-query'
import {api} from '../../lib/api'
import type {CDEnvironmentVariable} from '../delivery/types'
import {gitOpsPath, type ProjectCDConfiguration} from './queries'

// The CD form and Configuration/.env edit the same project-scoped draft.
export function useProjectCDEnvironment(nodeID: string, projectName: string, configuration: ProjectCDConfiguration | undefined, view: string) {
  const client = useQueryClient()
  const path = gitOpsPath(nodeID, projectName)
  const scope = `${nodeID}/${projectName}`
  const previousScope = useRef(scope)
  const [variables, setVariables] = useState<CDEnvironmentVariable[]>(configuration?.environment ?? [])
  const [baseline, setBaseline] = useState(JSON.stringify(configuration?.environment ?? []))
  const [expectedRevision, setExpectedRevision] = useState(configuration?.revision ?? 0)
  const [environmentValues, setValues] = useState<Record<string, string> | null>(null)
  const [showEnvironment, setRevealed] = useState(false)
  const [environmentError, setError] = useState('')
  const [environmentLoading, setLoading] = useState(false)
  const request = useRef<AbortController | null>(null)
  const dirty = JSON.stringify(variables) !== baseline

  useEffect(() => {
    if (previousScope.current !== scope || !dirty) {
      previousScope.current = scope
      setVariables(configuration?.environment ?? [])
      setBaseline(JSON.stringify(configuration?.environment ?? []))
      setExpectedRevision(configuration?.revision ?? 0)
    }
  }, [scope, configuration?.environment, configuration?.revision, dirty])

  useEffect(() => {
    request.current?.abort()
    setValues(null)
    setRevealed(false)
    setLoading(false)
    setError('')
    return () => request.current?.abort()
  }, [scope, configuration?.revision, view])

  const accept = useCallback((updated: ProjectCDConfiguration) => {
    request.current?.abort()
    setValues(null)
    setRevealed(false)
    setLoading(false)
    setVariables(updated.environment)
    setBaseline(JSON.stringify(updated.environment))
    setExpectedRevision(updated.revision)
    client.setQueryData(['project-gitops', nodeID, projectName], updated)
  }, [client, nodeID, projectName])

  const save = useMutation({
    mutationFn: () => api<ProjectCDConfiguration>(`${path}/environment`, {method: 'PUT', body: JSON.stringify({expected_revision: expectedRevision, environment: variables})}),
    onSuccess: async updated => {
      accept(updated)
      await client.invalidateQueries({queryKey: ['project-gitops', nodeID, projectName]})
    },
  })

  const toggleEnvironment = async () => {
    setError('')
    request.current?.abort()
    if (showEnvironment) {
      setValues(null)
      setRevealed(false)
      setLoading(false)
      return
    }
    if (!configuration?.enabled || !variables.some(variable => variable.has_value && variable.value === undefined)) {
      setRevealed(true)
      return
    }
    const controller = new AbortController()
    request.current = controller
    setLoading(true)
    try {
      const values = await api<Record<string, string>>(`${path}/environment/values`, {cache: 'no-store', signal: controller.signal})
      if (!controller.signal.aborted) {
        setValues(values)
        setRevealed(true)
      }
    } catch (error) {
      if (!controller.signal.aborted) setError(error instanceof Error ? error.message : String(error))
    } finally {
      if (!controller.signal.aborted) setLoading(false)
    }
  }

  return {variables, setVariables, expectedRevision, dirty, accept, save, values: {environmentValues, showEnvironment, environmentError, environmentLoading, toggleEnvironment}}
}

export type ProjectCDEnvironmentDraft = ReturnType<typeof useProjectCDEnvironment>

export function useProjectCDDraftGuard(dirty:boolean,zh:boolean){
 useBlocker({disabled:!dirty,enableBeforeUnload:dirty,shouldBlockFn:async()=>!(await confirmDialog({title:zh?'放弃未保存的 CD 配置？':'Discard unsaved CD settings?',danger:true}))})
 useEffect(()=>registerProjectNavigationGuard(async()=>!dirty || await confirmDialog({title:zh?'放弃未保存的 CD 配置并切换节点？':'Discard CD settings and switch node?',danger:true})),[dirty,zh])
}

