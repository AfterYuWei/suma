import { CircleAlert } from 'lucide-react'
import { Alert, AlertDescription, AlertTitle } from './alert'

export function ErrorState({ title, description, className = '' }: { title?: string; description: string; className?: string }) {
  return (
    <Alert variant="destructive" className={className}>
      <CircleAlert />
      {title ? <AlertTitle>{title}</AlertTitle> : null}
      <AlertDescription>{description}</AlertDescription>
    </Alert>
  )
}
