import { CircleAlert, CircleCheck, CircleX } from 'lucide-react'
import { Alert, AlertDescription, AlertTitle } from '../../components/ui/alert'
import { Spinner } from '../../components/ui/spinner'
import { useOpsText } from './helpers'
import type { AIConnectionTestResult } from './types'

export function AIConnectionTest({ result, error, pending, model }: { result?: AIConnectionTestResult; error: unknown; pending: boolean; model: string }) {
  const t = useOpsText()
  if (!pending && !error && !result) return null
  const failure = {
    request_failed: t('工具验证请求失败，请核对服务的 Responses 工具调用支持。', 'The tool verification request failed. Check the service’s Responses tool support.'),
    not_called: t('模型返回了响应，但没有调用测试工具。', 'The model responded without calling the test tool.'),
    unexpected_call: t('模型没有按要求返回一次带调用 ID 的 connection_probe 测试工具。', 'The model did not return exactly one connection_probe call with a call ID.'),
    invalid_arguments: t('测试工具的参数无效，预期为 {"message":"suma_connection_test"}。', 'The test tool arguments are invalid; {"message":"suma_connection_test"} was expected.'),
  }
  return <section aria-label={t('连接测试结果', 'Connection test result')} className="space-y-3">
    {pending ? <Alert role="status"><Spinner /><AlertTitle>{t('正在测试连接', 'Testing connection')}</AlertTitle><AlertDescription>{t('正在验证 Responses 文本响应和工具调用，请稍候…', 'Verifying Responses text and tool calling. Please wait…')}</AlertDescription></Alert> : error ? <Alert variant="destructive"><CircleX /><AlertTitle>{t('连接测试失败', 'Connection test failed')}</AlertTitle><AlertDescription className="break-words">{error instanceof Error ? error.message : String(error)}</AlertDescription></Alert> : result && <Alert role="status">
      {result.tool_capable ? <CircleCheck /> : <CircleAlert />}
      <AlertTitle>{result.tool_capable ? t('文本和工具调用通过', 'Text and tool calling verified') : t('连接正常，工具调用未通过', 'Connection works; tool verification did not pass')}</AlertTitle>
      <AlertDescription className="mt-2 space-y-3">
        <p className="break-all text-xs">{t('测试模型', 'Tested model')}：{result.model || model}{result.duration_ms !== undefined && <span> · {t('耗时', 'Duration')} {(result.duration_ms / 1000).toFixed(1)} s</span>}</p>
        <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm"><dt>{t('文本响应', 'Text response')}</dt><dd className={result.text ? 'text-emerald-600 dark:text-emerald-400' : 'text-destructive'}>{result.text ? t('通过', 'Passed') : t('未通过', 'Did not pass')}</dd><dt>{t('工具调用', 'Tool calling')}</dt><dd className={result.tool_capable ? 'text-emerald-600 dark:text-emerald-400' : 'text-destructive'}>{result.tool_capable ? t('通过', 'Passed') : t('未通过', 'Did not pass')}</dd></dl>
        {result.text_response && <div className="space-y-1"><p className="text-xs font-medium">{t('模型回复', 'Model reply')}</p><p className="whitespace-pre-wrap break-words rounded-md bg-muted/50 p-2 text-sm text-foreground">{result.text_response}</p></div>}
        {!result.tool_capable && <div className="space-y-1"><p>{result.tool_failure ? failure[result.tool_failure] : t('本次未通过工具验证，请确认模型支持 Responses 工具调用后重试。', 'Tool verification did not pass. Confirm that the model supports Responses tools and retry.')}</p>{result.tool_error && <p className="break-words text-xs">{result.tool_error}</p>}<p className="text-xs">{t('AI 当前仅生成摘要，无法使用诊断工具或提出变更操作。', 'AI currently produces summaries only and cannot use diagnostic tools or propose changes.')}</p></div>}
      </AlertDescription>
    </Alert>}
  </section>
}
