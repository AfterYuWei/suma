import { promptWithCheckboxDialog } from '../../stores/dialog'

export function confirmExternalProjectCleanup(projectName: string, zh: boolean) {
  return promptWithCheckboxDialog({
    title: zh ? '删除并清理外部 Project？' : 'Delete and clean external Project?',
    description: zh
      ? '将强制删除该 Compose Project 的全部容器和带有 Project 归属标签的网络。操作不可回滚，也可能因部分资源被占用而只完成一部分。不会读取或删除 bind mount 对应的宿主机目录。'
      : 'This force-removes all containers and Project-labeled networks in the Compose Project. It cannot be rolled back and may partially complete when resources are in use. Bind mount host directories are never read or deleted.',
    confirmLabel: zh ? '开始清理' : 'Start cleanup',
    danger: true,
    input: { label: zh ? `输入完整 Project Name：${projectName}` : `Type the complete Project Name: ${projectName}`, requiredValue: projectName },
    checkbox: {
      label: zh ? '高风险：同时永久删除 Project-owned 命名卷' : 'High risk: permanently delete Project-owned named volumes',
      description: zh ? '卷内数据不可恢复；被其他容器占用的卷不会被强制删除。' : 'Volume data cannot be recovered. Volumes used by other containers are not force-removed.',
    },
  })
}
