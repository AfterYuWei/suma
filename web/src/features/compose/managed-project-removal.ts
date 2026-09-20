import { promptWithCheckboxDialog } from '../../stores/dialog'

export function confirmManagedProjectRemoval(projectName: string, zh: boolean) {
  return promptWithCheckboxDialog({
    title: zh ? '删除 Compose Project？' : 'Delete Compose Project?',
    description: zh
      ? '将停止并删除该 Project 的容器、网络和孤立容器，然后删除 SUMA 托管的 Compose 文件。不会读取或删除 bind mount 对应的宿主机目录，命名卷默认保留。此操作不是取消接管。'
      : 'This stops and removes the Project containers, networks, and orphans, then deletes the SUMA-managed Compose files. Bind mount host directories are never read or deleted, and named volumes are preserved by default. This is not an undo-takeover action.',
    confirmLabel: zh ? '删除项目' : 'Delete Project',
    danger: true,
    input: { label: zh ? `输入完整 Project Name：${projectName}` : `Type the complete Project Name: ${projectName}`, requiredValue: projectName },
    checkbox: {
      label: zh ? '高风险：同时永久删除项目命名卷' : 'High risk: permanently delete Project named volumes',
      description: zh ? '卷内数据不可恢复；保持未勾选将保留全部命名卷。' : 'Volume data cannot be recovered. Leave this unchecked to preserve all named volumes.',
    },
  })
}
