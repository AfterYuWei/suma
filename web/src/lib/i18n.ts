import { useUIStore, type Language } from '../stores/ui'

const zh = {
  overview: '概览', containers: '容器', compose: 'Compose', continuousDelivery: '持续交付', images: '镜像', networks: '网络', volumes: '存储卷',
  operations: '运维', tasks: '任务', auditLogs: '审计日志', system: '系统', authenticationCenter: '认证中心', settings: '设置', nodes: '节点', dockerConnected: 'Docker 已连接',
  searchCommand: '搜索或执行命令', signOut: '退出登录', cancel: '取消', confirm: '确认', remove: '删除', create: '创建', save: '保存',
  language: '语言', chinese: '中文', english: 'English', appearance: '外观', chooseTheme: '选择界面主题。',
  dark: '深色', light: '浅色', systemTheme: '跟随系统', loading: '加载中…', retry: '重试',
  signOutTitle: '退出 SUMA？', signOutDescription: '当前浏览器会话将被注销。',
  dangerWarning: '此操作可能无法撤销，请确认后继续。', typeToConfirm: '输入 {value} 以确认',
  newImageTag: '新镜像标签', tagDescription: '输入 repository:tag 格式的新标签。', tag: '添加标签',
  removeImage: '删除镜像', removeImageDescription: '确定删除镜像 {name}？正在使用的镜像不会被强制删除。',
  deleteNetwork: '删除网络', deleteNetworkDescription: '确定删除网络 {name}？此操作无法撤销。',
  deleteVolume: '永久删除存储卷', deleteVolumeDescription: '这会永久删除 {name} 中的全部数据。',
  newProject: '新建项目', projectName: '项目名称', projectNameDescription: '输入新的 Compose 项目名称。',
  systemPrune: '系统清理', systemPruneDescription: '将删除未使用的容器、网络、悬空镜像和匿名存储卷。',
  renameContainer: '重命名容器', newContainerName: '新容器名称',
  removeContainer: '永久删除容器', removeContainerDescription: '确定删除 {name}？此操作无法撤销。',
  killContainer: '强制终止容器', killContainerDescription: '立即终止 {name} 的主进程？',
  deployChanges: '保存并部署更改', deployChangesDescription: '即将更新 {files} 并重新部署项目。',
  removeProject: '删除 Compose 项目', removeProjectDescription: '将删除项目记录和文件，但不会删除仍在运行的容器。',
  composeDown: '停止并移除项目资源', composeDownDescription: 'Compose down 将删除 {name} 的容器和网络。',
  general: '常规', storage: '存储', security: '安全', registry: '镜像仓库', serverName: '服务器名称', timezone: '时区',
  dockerSocket: 'Docker Socket', composeCommand: 'Compose 命令', composeRoot: 'Compose 根目录', dataRoot: '数据目录', backupRoot: '备份目录',
  secureCookies: '安全 Cookie', defaultRegistry: '默认镜像仓库', localConfiguration: 'SUMA 本地配置', saveChanges: '保存更改', settingsSaved: '设置已保存',
} as const

export type TranslationKey = keyof typeof zh
type Variables = Record<string, string | number>

export function translate(language: Language, key: TranslationKey, variables: Variables = {}) {
  const template = language === 'zh-CN' ? zh[key] : english[key]
  return Object.entries(variables).reduce((text, [name, value]) => text.replaceAll(`{${name}}`, String(value)), template)
}

const english: Record<TranslationKey, string> = {
  overview: 'Overview', containers: 'Containers', compose: 'Compose', continuousDelivery: 'Continuous Delivery', images: 'Images', networks: 'Networks', volumes: 'Volumes',
  operations: 'Operations', tasks: 'Tasks', auditLogs: 'Audit logs', system: 'System', authenticationCenter: 'Authentication Center', settings: 'Settings', nodes: 'Nodes', dockerConnected: 'Docker connected',
  searchCommand: 'Search or run a command', signOut: 'Sign out', cancel: 'Cancel', confirm: 'Confirm', remove: 'Remove', create: 'Create', save: 'Save',
  language: 'Language', chinese: '中文', english: 'English', appearance: 'Appearance', chooseTheme: 'Choose the interface theme.',
  dark: 'Dark', light: 'Light', systemTheme: 'System', loading: 'Loading…', retry: 'Retry',
  signOutTitle: 'Sign out of SUMA?', signOutDescription: 'The current browser session will be signed out.',
  dangerWarning: 'This action may not be reversible. Confirm before continuing.', typeToConfirm: 'Type {value} to confirm',
  newImageTag: 'New image tag', tagDescription: 'Enter a new tag in repository:tag format.', tag: 'Tag image',
  removeImage: 'Remove image', removeImageDescription: 'Remove image {name}? Images in use will not be forced.',
  deleteNetwork: 'Delete network', deleteNetworkDescription: 'Delete network {name}? This cannot be undone.',
  deleteVolume: 'Permanently delete volume', deleteVolumeDescription: 'This permanently deletes all data in {name}.',
  newProject: 'New project', projectName: 'Project name', projectNameDescription: 'Enter a name for the new Compose project.',
  systemPrune: 'System prune', systemPruneDescription: 'Unused containers, networks, dangling images, and anonymous volumes will be removed.',
  renameContainer: 'Rename container', newContainerName: 'New container name',
  removeContainer: 'Permanently remove container', removeContainerDescription: 'Remove {name}? This cannot be undone.',
  killContainer: 'Force kill container', killContainerDescription: 'Immediately kill the main process in {name}?',
  deployChanges: 'Save and deploy changes', deployChangesDescription: 'The following files will be updated before deployment: {files}.',
  removeProject: 'Remove Compose project', removeProjectDescription: 'The project record and files will be deleted. Running containers are not removed.',
  composeDown: 'Stop and remove project resources', composeDownDescription: 'Compose down will remove containers and networks for {name}.',
  general: 'General', storage: 'Storage', security: 'Security', registry: 'Registry', serverName: 'Server name', timezone: 'Timezone',
  dockerSocket: 'Docker socket', composeCommand: 'Compose command', composeRoot: 'Compose root', dataRoot: 'Data root', backupRoot: 'Backup root',
  secureCookies: 'Secure cookies', defaultRegistry: 'Default registry', localConfiguration: 'Local SUMA configuration', saveChanges: 'Save changes', settingsSaved: 'Settings saved',
}

export function useI18n() {
  const language = useUIStore((state) => state.language)
  return { language, t: (key: TranslationKey, variables?: Variables) => translate(language, key, variables) }
}
