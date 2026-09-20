import { confirmDialog } from '../../stores/dialog'

export const dockerSocketConfirmationHeader = 'X-SUMA-Allow-Docker-Socket'

export function composeMountsDockerSocket(compose: string) {
  return compose.includes('/var/run/docker.sock')
}

export function dockerSocketHeaders(allowed: boolean): HeadersInit | undefined {
  return allowed ? { [dockerSocketConfirmationHeader]: 'true' } : undefined
}

// Returns false when no privileged mount is present, true after both warnings,
// and null when the operator cancels either confirmation.
export async function confirmDockerSocketMount(compose: string, zh: boolean): Promise<boolean | null> {
  if (!composeMountsDockerSocket(compose)) return false
  const first = await confirmDialog({
    title: zh ? '允许容器访问 Docker Socket？' : 'Allow container access to the Docker socket?',
    description: zh ? '挂载 /var/run/docker.sock 会让容器拥有对该 Docker 节点的完全控制权，包括创建特权容器、读取宿主机文件和删除资源。' : 'Mounting /var/run/docker.sock gives the container full control of this Docker node, including creating privileged containers, reading host files, and deleting resources.',
    confirmLabel: zh ? '继续' : 'Continue',
    danger: true,
  })
  if (!first) return null
  const second = await confirmDialog({
    title: zh ? '再次确认 Docker Socket 挂载' : 'Confirm Docker socket mount again',
    description: zh ? '这等同于将节点管理权交给该容器。仅当你完全信任镜像及其内部程序时才允许。' : 'This effectively gives the container administrative control of the node. Allow it only when you fully trust the image and everything running inside it.',
    confirmLabel: zh ? '允许挂载' : 'Allow mount',
    danger: true,
  })
  return second ? true : null
}
