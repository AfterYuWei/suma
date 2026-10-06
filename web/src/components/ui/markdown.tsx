import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

export function Markdown({ children }: { children: string }) {
  return <div data-slot="markdown" className="min-w-0 break-words text-sm leading-7 [&>*:first-child]:mt-0 [&>*:last-child]:mb-0 [&_p]:my-3 [&_h1]:mt-6 [&_h1]:mb-3 [&_h1]:text-xl [&_h1]:font-semibold [&_h2]:mt-5 [&_h2]:mb-2 [&_h2]:text-lg [&_h2]:font-semibold [&_h3]:mt-4 [&_h3]:font-semibold [&_h4]:mt-3 [&_h4]:font-semibold [&_ul]:my-3 [&_ul]:list-disc [&_ul]:pl-5 [&_ol]:my-3 [&_ol]:list-decimal [&_ol]:pl-5 [&_li]:my-1 [&_li>p]:my-1 [&_blockquote]:my-3 [&_blockquote]:border-l-2 [&_blockquote]:pl-4 [&_blockquote]:text-muted-foreground [&_hr]:my-4 [&_code]:rounded [&_code]:bg-muted [&_code]:px-1 [&_code]:py-0.5 [&_code]:text-xs [&_pre]:my-3 [&_pre]:max-w-full [&_pre]:overflow-x-auto [&_pre]:rounded-md [&_pre]:bg-muted [&_pre]:p-3 [&_pre]:leading-6 [&_pre_code]:bg-transparent [&_pre_code]:p-0 [&_th]:border [&_th]:bg-muted/50 [&_th]:px-3 [&_th]:py-1.5 [&_th]:text-left [&_td]:border [&_td]:px-3 [&_td]:py-1.5 [&_.contains-task-list]:list-none [&_.task-list-item_input]:mr-2">
    <ReactMarkdown remarkPlugins={[remarkGfm]} skipHtml components={{
      a: ({ href, children }) => href ? <a href={href} target="_blank" rel="noopener noreferrer" className="underline underline-offset-4 hover:text-muted-foreground">{children}</a> : <span>{children}</span>,
      table: ({ children }) => <div className="my-3 max-w-full overflow-x-auto overscroll-contain"><table className="w-full border-collapse text-xs">{children}</table></div>,
      // Model-supplied media must not make automatic requests to third parties.
      img: ({ alt }) => <span className="text-muted-foreground">{alt}</span>,
    }}>{children}</ReactMarkdown>
  </div>
}
