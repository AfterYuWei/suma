export interface CreateContainerRequest {
  name: string; image: string; pull_policy: 'missing' | 'always' | 'never'; credential_id?: number; start: boolean
  ports: { container_port: number; host_port: number; host_ip: string; protocol: 'tcp' | 'udp' }[]
  environment: { key: string; value: string }[]
  mounts: { type: 'bind' | 'volume'; source: string; target: string; read_only: boolean }[]
  network: string; restart_policy: string; command: string[]; entrypoint: string[]; working_directory: string; user: string
  memory_mb: number; cpus: number; read_only_rootfs: boolean; tty: boolean; open_stdin: boolean
}

export interface ContainerCreateDraft extends Omit<CreateContainerRequest, 'command' | 'entrypoint' | 'memory_mb' | 'cpus'> {
  command: string; entrypoint: string; memory_mb: string; cpus: string
}
export const emptyContainerDraft = (): ContainerCreateDraft => ({ name: '', image: '', pull_policy: 'missing', start: true, ports: [], environment: [], mounts: [], network: 'bridge', restart_policy: 'unless-stopped', command: '', entrypoint: '', working_directory: '', user: '', memory_mb: '', cpus: '', read_only_rootfs: false, tty: false, open_stdin: false })

export function containerCreateRequest(draft: ContainerCreateDraft, zh: boolean): CreateContainerRequest {
  const fail = (cn: string, en: string): never => { throw new Error(zh ? cn : en) }
  const absolute = (value: string) => value.startsWith('/') && !/(?:^|\/)\.{1,2}(?:\/|$)|\/\/|[\0\r\n]/.test(value) && (value === '/' || !value.endsWith('/'))
  if (!/^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$/.test(draft.name.trim())) fail('容器名称只能包含字母、数字、点、下划线和连字符。', 'Use letters, numbers, dots, underscores and hyphens for the container name.')
  if (!draft.image.trim() || /\s|:\/\//.test(draft.image.trim())) fail('请填写有效的镜像引用。', 'Enter a valid image reference.')
  if (['host', 'none'].includes(draft.network) && draft.ports.length) fail('host／none 网络不能发布端口，请移除端口映射或更换网络。', 'Host/none networks cannot publish ports. Remove the mappings or change the network.')
  const bindings = new Set<string>()
  for (const port of draft.ports) {
    if (!Number.isInteger(port.container_port) || port.container_port < 1 || port.container_port > 65535 || !Number.isInteger(port.host_port) || port.host_port < 0 || port.host_port > 65535 || !port.host_ip.trim()) fail('端口必须在 1–65535 范围内；宿主机端口为 0 表示自动分配。', 'Ports must be 1–65535; host port 0 assigns an available port.')
    const binding = `${port.host_ip}:${port.host_port}/${port.protocol}`
    if (port.host_port && bindings.has(binding)) fail('宿主机端口映射重复。', 'A host port mapping is duplicated.')
    bindings.add(binding)
  }
  const variables = new Set<string>()
  for (const variable of draft.environment) {
    if (!/^[a-zA-Z_][a-zA-Z0-9_]*$/.test(variable.key) || variables.has(variable.key)) fail('环境变量名称无效或重复。', 'An environment variable name is invalid or duplicated.')
    variables.add(variable.key)
  }
  const targets = new Set<string>()
  for (const mount of draft.mounts) {
    if (!absolute(mount.target) || mount.target === '/' || targets.has(mount.target)) fail('挂载目标必须是互不重复的绝对路径，且不能为根目录。', 'Mount targets must be distinct absolute paths below root.')
    targets.add(mount.target)
    if (mount.type === 'bind' ? !absolute(mount.source) || mount.source.includes('$') : !/^[a-zA-Z0-9][a-zA-Z0-9_.-]*$/.test(mount.source)) fail('绑定挂载需使用节点上的非插值绝对路径；存储卷需填写有效名称。', 'Bind mounts need non-interpolated absolute node paths; named volumes need a valid name.')
  }
  if (draft.working_directory && !absolute(draft.working_directory)) fail('工作目录必须是绝对路径。', 'Working directory must be an absolute path.')
  const memory = Number(draft.memory_mb), cpus = Number(draft.cpus)
  if (!Number.isInteger(memory) || memory < 0 || memory > 2 ** 30 || memory > 0 && memory < 6 || !Number.isFinite(cpus) || cpus < 0 || cpus > 1024) fail('内存至少为 6 MiB，CPU 范围为 0–1024；留空表示不限制。', 'Memory must be at least 6 MiB and CPUs 0–1024; leave blank for no limit.')
  const args = (value: string) => value === '' ? [] : value.split('\n').map(arg => arg.replace(/\r$/, ''))
  return { ...draft, name: draft.name.trim(), image: draft.image.trim(), ports: draft.ports.map(port => ({ ...port, host_ip: port.host_ip.trim() })), command: args(draft.command), entrypoint: args(draft.entrypoint), memory_mb: memory, cpus }
}

export const mountsDockerSocket = (mounts: CreateContainerRequest['mounts']) => mounts.some(mount => [mount.source, mount.target].some(value => value === '/var/run/docker.sock' || value === '/run/docker.sock'))
