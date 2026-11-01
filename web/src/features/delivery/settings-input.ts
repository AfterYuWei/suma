import {choiceDialog} from '../../stores/dialog'
import type {CDConfigureInput} from './types'

export async function prepareCDSettingsInput(draft: CDConfigureInput, composeFiles: string[], zh: boolean, creating = false): Promise<CDConfigureInput | null> {
  const files = composeFiles.map((file) => file.trim())
  if (!files.length || files.some((file) => !file)) {
    throw new Error(zh ? '请填写所有 Compose 文件路径。' : 'Complete every Compose file path.')
  }
  const variables = draft.environment ?? []
  if (variables.length > 100 || variables.some(variable => !/^[A-Za-z_][A-Za-z0-9_]{0,255}$/.test(variable.name)) || new Set(variables.map(variable => variable.name)).size !== variables.length) {
    throw new Error(zh ? '环境变量名称须为字母、数字或下划线，不能以数字开头或重复，最多 100 项。' : 'Use unique variable names with letters, numbers and underscores, starting with a letter or underscore; at most 100 variables.')
  }
  let authentication = draft.repository.authentication
  if (authentication.source === 'project' && authentication.credential) {
    const material = authentication.credential
    const summary = authentication.summary
    const changed = !!(material.secret || material.private_key || material.passphrase || material.known_hosts || material.custom_ca || !summary || material.name !== summary.name || material.auth_type !== summary.auth_type || material.username !== (summary.username || ''))
    if (changed) {
      const choice = creating ? (authentication.save_to_center ? 'center' : 'project') : await choiceDialog({ title: zh ? '保存项目认证凭据' : 'Save project credential', description: zh ? '该凭据可以仅由当前项目使用，也可以保存到认证中心供其他功能选择。' : 'Keep this credential private to the current project, or save it to the Authentication Center for reuse.', choices: [{ value: 'project', label: zh ? '仅保存到此项目' : 'Only this project' }, { value: 'center', label: zh ? '保存到认证中心并使用' : 'Save to center and use', primary: true }] })
      if (!choice) return null
      authentication = { ...authentication, save_to_center: choice === 'center', credential: choice === 'center' ? { ...material, authorized_node_ids: draft.node_ids } : material }
    } else {
      authentication = { source: 'project' }
    }
  } else if (authentication.source === 'project') {
    authentication = { source: 'project' }
  }
  return { ...draft, environment: variables.map(variable => variable.has_value || variable.value !== undefined ? variable : { ...variable, value: '' }), repository: { ...draft.repository, authentication, compose_files: files } }
}

