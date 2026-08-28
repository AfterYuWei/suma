import { AlertTriangle } from 'lucide-react'
import { Button } from '../../components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '../../components/ui/dialog'

export function TakeoverWarningDialog({ open, projectName, zh, onOpenChange, onContinue }: { open: boolean; projectName: string; zh: boolean; onOpenChange: (open: boolean) => void; onContinue: () => void }) {
  return <Dialog open={open} onOpenChange={onOpenChange}>
    <DialogContent className="sm:max-w-lg">
      <DialogHeader>
        <div className="mb-1 flex size-10 items-center justify-center rounded-lg bg-amber-500/10 text-amber-600 dark:text-amber-400"><AlertTriangle /></div>
        <DialogTitle>{zh ? `接管 Project：${projectName}` : `Take over Project: ${projectName}`}</DialogTitle>
        <DialogDescription>{zh ? 'SUMA 将根据安全可访问的 Compose 配置或 Docker 运行态生成新的托管配置。' : 'SUMA will generate a managed configuration from safely accessible Compose files or Docker runtime metadata.'}</DialogDescription>
      </DialogHeader>
      <ul className="list-disc space-y-2 pl-5 text-sm text-muted-foreground">
        <li>{zh ? '接管只保存配置，不会立即拉取镜像、重建或停止现有容器。' : 'Takeover only saves configuration; it does not pull, recreate, or stop existing containers.'}</li>
        <li>{zh ? '规范化后不会保留原 YAML 的注释、锚点、多文件结构和原始变量表达式。' : 'Normalization cannot preserve comments, anchors, multi-file layout, or original variable expressions.'}</li>
        <li>{zh ? '首次由 SUMA 部署时可能重建容器或改变运行资源，届时会再次确认。' : 'The first SUMA deployment may recreate containers or change runtime resources and will ask again.'}</li>
        <li>{zh ? '运行态重建不能保证 100% 还原原始意图，请在接管前复核草稿和风险。' : 'Runtime reconstruction cannot guarantee the original intent; review the draft and risks before takeover.'}</li>
      </ul>
      <DialogFooter>
        <Button variant="outline" onClick={() => onOpenChange(false)}>{zh ? '取消' : 'Cancel'}</Button>
        <Button onClick={onContinue}>{zh ? '了解并开始分析' : 'Understand and analyze'}</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
}
