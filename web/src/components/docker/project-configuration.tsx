import {
  lazy,
  Suspense,
  useEffect,
  useCallback,
  useId,
  useMemo,
  useRef,
  useState,
  type ReactNode
} from 'react'
import {
  Box,
  Code2,
  Eye,
  EyeOff,
  FileCode2,
  Plus,
  Trash2,
  X
} from 'lucide-react'
import { Button } from '../ui/button'
import { Input } from '../ui/input'
import { Textarea } from '../ui/textarea'
import { Label } from '../ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue
} from "@/components/ui/select"
import { Tabs, TabsContent, TabsList, TabsTrigger } from '../ui/tabs'
import { Alert, AlertDescription } from '../ui/alert'
import { TooltipHint } from '../ui/tooltip-hint'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle
} from '../ui/dialog'
import { Spinner } from '../ui/spinner'
import { confirmDialog, promptDialog } from '../../stores/dialog'
import { useUIStore } from '../../stores/ui'
import { AddNetworkDialog, MountForm } from './project-resource-dialogs'
import { PortMappings } from './project-port-mappings'
import {
  addService,
  nextUnnamedServiceName,
  unnamedServiceNumber,
  normalizeImage,
  composeProblems,
  configurationIssues,
  configKeys,
  configValue,
  editConfig,
  envEntries,
  maskCompose,
  maskEnvironment,
  readonlyReason,
  readonlyGroupReason,
  references,
  REMOVE,
  renameService,
  sensitiveKey,
  setEnv,
  sourceLine,
  variableReferences,
  type ConfigPath
} from '../../features/compose/document'

const Monaco = lazy(() => import('../../lib/monaco-editor'))
const text = (value: unknown) => (value == null ? '' : String(value))
const record = (value: unknown): Record<string, unknown> =>
  value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {}
const serviceLabel = (name: string, zh: boolean) => {
  const number = unnamedServiceNumber(name)
  return number === undefined ? name : zh ? `未命名${number}` : `Unnamed ${number}`
}

function FieldCaption({ zh, chinese, english, field }: { zh: boolean; chinese: string; english: string; field: string }) {
  return <span className="inline-flex flex-wrap items-baseline gap-x-1.5">{zh ? chinese : english}{' '}<span className="text-xs font-normal text-muted-foreground">{field}</span></span>
}
function TabCaption({ zh, chinese, english }: { zh: boolean; chinese: string; english: string }) {
  return zh ? <span className="flex flex-col items-start gap-0.5 leading-tight"><span>{chinese}</span><span className="text-[10px] font-normal text-muted-foreground">{english}</span></span> : english
}

export function Choice({
  label,
  value,
  options,
  onChange,
  disabled
}: {
  label: ReactNode
  value: string
  options: [string, string][]
  onChange: (value: string) => void
  disabled?: boolean
}) {
  const id = useId()
  return (
    <div className="min-w-0 flex flex-col gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Select
        items={options.map(([value, label]) => ({ value, label }))}
        value={value}
        onValueChange={(next) => onChange(String(next))}
        disabled={disabled}
      >
        <SelectTrigger id={id} className="w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent align="start" alignItemWithTrigger={false} className="min-w-0">
          <SelectGroup>
          {options.map(([key, name]) => (
            <SelectItem key={key} value={key}>
              {name}
            </SelectItem>
          ))}
          </SelectGroup>
        </SelectContent>
      </Select>
    </div>
  )
}
export function ValueInput({
  label,
  value,
  onChange,
  secret = false,
  disabled = false,
  multiline = false,
  placeholder
}: {
  label: ReactNode
  value: string
  onChange: (value: string) => void
  secret?: boolean
  disabled?: boolean
  multiline?: boolean
  placeholder?: string
}) {
  const id = useId(),
    [draft, setDraft] = useState(value),
    [reveal, setReveal] = useState(false)
  useEffect(() => setDraft(value), [value])
  return (
    <div className="min-w-0 space-y-1.5">
      <Label htmlFor={id}>{label}</Label>
      <div className="flex items-center gap-1">
        {multiline && !secret ? (
          <Textarea
            id={id}
            value={draft}
            disabled={disabled}
            placeholder={placeholder}
            onChange={(event) => setDraft(event.target.value)}
            onBlur={() => {
              if (draft !== value) onChange(draft)
            }}
          />
        ) : (
          <Input
            id={id}
            type={secret && !reveal ? 'password' : 'text'}
            autoComplete="off"
            value={draft}
            disabled={disabled}
            placeholder={placeholder}
            onChange={(event) => setDraft(event.target.value)}
            onBlur={() => {
              if (draft !== value) onChange(draft)
            }}
          />
        )}
        {secret && (
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={reveal ? 'Hide / 隐藏' : 'Reveal / 显示'}
            onClick={() => setReveal(!reveal)}
          >
            {reveal ? <EyeOff /> : <Eye />}
          </Button>
        )}
      </div>
    </div>
  )
}
function DeleteButton({ onClick, zh }: { onClick: () => void; zh: boolean }) {
  return (
    <TooltipHint content={zh ? '删除配置' : 'Remove configuration'}>
      <Button
        variant="ghost"
        size="icon-sm"
        className="shrink-0 text-destructive"
        aria-label={zh ? '删除配置' : 'Remove configuration'}
        onClick={onClick}
      >
        <Trash2 />
      </Button>
    </TooltipHint>
  )
}

interface EditorProps {
  nodeID: string
  compose: string
  environment: string
  onCompose: (value: string) => void
  onEnvironment: (value: string) => void
  zh: boolean
  disabled?: boolean
  location?: ConfigPath
  onPendingChange?: (pending: boolean) => void
}
interface FormContext extends EditorProps {
  service: string
  displayService: string
  selectService: (name: string) => void
  set: (path: ConfigPath, value: unknown | typeof REMOVE) => void
  locate: (path: ConfigPath, file?: 'compose' | 'environment') => void
  fail: (error: unknown) => void
  pending: (key: string, pending: boolean) => void
  rename: (name: string) => void
}

export function ProjectConfiguration(props: EditorProps) {
  const {
    compose,
    environment,
    onCompose,
    onEnvironment,
    zh,
    disabled = false
  } = props
  const [pendingStatus, setPendingStatus] = useState(false)
  const pendingKeys = useRef(new Set<string>())
  const pendingCallback = useRef(props.onPendingChange)
  pendingCallback.current = props.onPendingChange
  const pendingForm = useCallback((key: string, pending: boolean) => {
    if (pending) pendingKeys.current.add(key)
    else pendingKeys.current.delete(key)
    setPendingStatus(pendingKeys.current.size > 0)
    pendingCallback.current?.(pendingKeys.current.size > 0)
  }, [])
  const dark = useUIStore((state) => state.resolvedDark)
  const [mode, setMode] = useState('visual'),
    [focus, setFocus] = useState(''),
    [serviceTab, setServiceTab] = useState('basics'),
    [preview, setPreview] = useState(false),
    [file, setFile] = useState<'compose' | 'environment'>('compose'),
    [reveal, setReveal] = useState(false),
    [notice, setNotice] = useState(''),
    [line, setLine] = useState(1)
  const unnamedSequence = useRef(1)
  const composeRef = useRef(compose)
  composeRef.current = compose
  useEffect(() => {
    if (props.location) {
      setMode('source')
      setFile('compose')
      setLine(sourceLine(composeRef.current, props.location))
      if (props.location[0] === 'services' && typeof props.location[1] === 'string') {
        setFocus(props.location[1])
      }
    }
  }, [props.location])
  const services = configKeys(compose, ['services']),
    problems = composeProblems(compose),
    issues = useMemo(() => configurationIssues(compose), [compose])
  const fail = (error: unknown) =>
    setNotice(error instanceof Error ? error.message : String(error))
  const set = (path: ConfigPath, value: unknown | typeof REMOVE) => {
    try {
      onCompose(editConfig(compose, path, value))
      setNotice('')
    } catch (error) {
      fail(error)
    }
  }
  const locate = (
    path: ConfigPath,
    next: 'compose' | 'environment' = 'compose'
  ) => {
    void changeWorkspace(() => {
      if (path[0] === 'services' && typeof path[1] === 'string') selectService(path[1])
      setFile(next)
      setMode('source')
      setLine(next === 'compose' ? sourceLine(compose, path) : 1)
    })
  }
  const source = file === 'compose' ? compose : environment
  const masked =
    file === 'compose' ? maskCompose(source) : maskEnvironment(source)
  const hidden = !reveal && masked !== source
  const sourceEditor = (readOnly: boolean) => (
    <div className="h-[58vh] min-h-80 overflow-hidden rounded-md border">
      <Suspense fallback={<Spinner />}>
        <Monaco
          language={file === 'compose' ? 'yaml' : 'ini'}
          theme={dark ? 'vs-dark' : 'light'}
          value={reveal ? source : masked}
          onChange={
            readOnly || hidden || disabled
              ? undefined
              : (value) => {
                  setReveal(true)
                  if (file === 'compose') onCompose(value ?? '')
                  else onEnvironment(value ?? '')
                }
          }
          revealLine={line}
          options={{
            readOnly: readOnly || hidden || disabled,
            // Avoid Monaco's pending word-highlight cancellation on mode switches.
            occurrencesHighlight: 'off',
            automaticLayout: true,
            minimap: { enabled: false },
            wordWrap: 'on',
            scrollBeyondLastLine: false,
            fontSize: 12
          }}
        />
      </Suspense>
    </div>
  )
  const navigation: [string, string][] = services.map((name) => [name, serviceLabel(name, zh)])
  const requestedService = focus.startsWith('service:') ? focus.slice(8) : focus
  const service = services.includes(requestedService) ? requestedService : services[0] ?? ''
  const selectService = (name: string) => {
    setFocus(name)
    setServiceTab('basics')
  }
  const unnamedName = () => {
    const name = nextUnnamedServiceName(compose, unnamedSequence.current)
    unnamedSequence.current = (unnamedServiceNumber(name) ?? unnamedSequence.current) + 1
    return name
  }
  const createService = () => {
    try {
      const name = unnamedName()
      onCompose(addService(compose, name))
      selectService(name)
      setNotice('')
    } catch (error) { fail(error) }
  }
  const changeWorkspace = async (change: () => void) => {
    if (pendingKeys.current.size > 0 && !(await confirmDialog({
      title: zh ? '放弃尚未提交的表单？' : 'Discard the unfinished form?',
      description: zh ? '已写入配置的内容会保留。请先添加或保存当前映射，也可以取消后继续编辑。' : 'Committed configuration stays intact. Apply the current mapping first, or cancel to keep editing.',
      danger: true
    }))) return
    change()
  }
  const ctx: FormContext = {
    ...props,
    service,
    displayService: serviceLabel(service, zh),
    selectService,
    set,
    locate,
    fail,
    pending: pendingForm,
    rename: (name) => {
      const next = name.trim() || unnamedName()
      onCompose(renameService(compose, service, next))
      selectService(next)
      setNotice('')
    }
  }
  return (
    <div
      className="w-full min-w-0 space-y-3"
      data-testid="project-configuration"
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <Tabs value={mode} onValueChange={value => void changeWorkspace(() => setMode(value))}>
          <TabsList variant="line">
            <TabsTrigger value="visual">{zh ? '可视化' : 'Visual'}</TabsTrigger>
            <TabsTrigger value="source">Compose</TabsTrigger>
          </TabsList>
        </Tabs>
        <div className="flex items-center gap-2">
          <Button size="sm" variant="ghost" onClick={() => setReveal(!reveal)}>
            {reveal ? <EyeOff /> : <Eye />}
            {reveal
              ? zh
                ? '隐藏敏感源码'
                : 'Hide sensitive source'
              : zh
                ? '显示敏感源码'
                : 'Reveal sensitive source'}
          </Button>
          {mode === 'visual' && (
            <Button
              size="sm"
              variant="outline"
              className="hidden xl:inline-flex"
              onClick={() => setPreview(!preview)}
            >
              <Code2 />
              {preview
                ? zh
                  ? '收起预览'
                  : 'Hide preview'
                : zh
                  ? '源码预览'
                  : 'Source preview'}
            </Button>
          )}
        </div>
      </div>
      {pendingStatus && <p className="text-xs text-muted-foreground" data-testid="pending-configuration-form">{zh ? '有尚未提交的表单，请先完成或取消，再校验、保存及部署。' : 'Finish or cancel the unfinished form before validating, saving or deploying.'}</p>}
      {notice && (
        <Alert variant="destructive">
          <AlertDescription>{notice}</AlertDescription>
        </Alert>
      )}
      {!!issues.length && !problems.length && (
        <Alert>
          <AlertDescription>
            <details>
              <summary className="cursor-pointer">
                {zh
                  ? `配置问题 · ${issues.length}`
                  : `Configuration issues · ${issues.length}`}
              </summary>
              <div className="mt-2 flex flex-col items-start gap-1">
                {issues.map((issue, i) => (
                  <Button
                    key={i}
                    variant="link"
                    size="sm"
                    className="h-auto text-left whitespace-normal"
                    onClick={() => {
                      locate(issue.path)
                    }}
                  >
                    {issue.path.join('.')} ·{' '}
                    {
                      {
                        image: zh
                          ? '需要 image 或 build'
                          : 'Image or build required',
                        port: zh
                          ? '目标端口须为 1–65535'
                          : 'Target port must be 1–65535',
                        mount: zh
                          ? '容器路径须为绝对路径'
                          : 'Container path must be absolute',
                        dependency: zh
                          ? '依赖服务不存在或指向自身'
                          : 'Dependency is missing or refers to itself'
                      }[issue.code]
                    }
                  </Button>
                ))}
              </div>
            </details>
          </AlertDescription>
        </Alert>
      )}
      {!!problems.length && (
        <Alert variant="destructive">
          <AlertDescription>
            {problems.map((problem, i) => (
              <Button
                key={i}
                variant="link"
                onClick={() => {
                  setMode('source')
                  setFile('compose')
                  setLine(problem.line)
                }}
              >
                {problem.message} · {problem.line}:{problem.column}
              </Button>
            ))}
          </AlertDescription>
        </Alert>
      )}
      {mode === 'source' ? (
        <div className="space-y-2">
          <Tabs
            value={file}
            onValueChange={(value) => {
              setFile(value as typeof file)
              setLine(1)
            }}
          >
            <TabsList>
              <TabsTrigger value="compose">compose.yml</TabsTrigger>
              <TabsTrigger value="environment">.env</TabsTrigger>
            </TabsList>
          </Tabs>
          {hidden && (
            <p className="text-xs text-muted-foreground">
              {zh
                ? '敏感值已遮罩。显示敏感源码后可编辑原文。'
                : 'Sensitive values are masked. Reveal sensitive source to edit the original.'}
            </p>
          )}
          {sourceEditor(false)}
        </div>
      ) : (
        <div
          className={`grid min-w-0 gap-3 ${preview ? 'xl:grid-cols-[minmax(0,1fr)_minmax(320px,0.65fr)]' : ''}`}
        >
          <div className="grid min-w-0 gap-5 md:grid-cols-[210px_minmax(0,1fr)]">
            <div className="space-y-3 md:border-r md:pr-4" data-testid="project-service-navigation">
              <div className="hidden items-center justify-between text-xs text-muted-foreground md:flex">
                <span>{zh ? '服务' : 'Services'}</span><span>{services.length}</span>
              </div>
              <div className="md:hidden">
                {!!services.length && <Choice
                  label={zh ? '当前服务' : 'Current service'}
                  value={service}
                  options={navigation}
                  onChange={name => void changeWorkspace(() => selectService(name))}
                />}
              </div>
              <nav aria-label={zh ? '项目服务' : 'Project services'} className="hidden max-h-[55vh] space-y-1 overflow-y-auto overscroll-contain md:block">
                {navigation.map(([key, label]) => (
                  <Button
                    key={key}
                    aria-label={label}
                    aria-current={service === key ? 'page' : undefined}
                    variant={service === key ? 'secondary' : 'ghost'}
                    className="h-auto w-full justify-start gap-2 py-2 text-left"
                    onClick={() => void changeWorkspace(() => selectService(key))}
                  >
                    <Box className="size-4 shrink-0" />
                    <span className="grid min-w-0 gap-0.5"><span className="truncate">{label}</span><span className="truncate text-xs font-normal text-muted-foreground">{text(configValue(compose, ['services', key, 'image'])) || (configValue(compose, ['services', key, 'build']) !== undefined ? zh ? '源码配置' : 'Source configuration' : zh ? '未填写镜像' : 'Image not set')}</span></span>
                    {readonlyReason(compose, ['services', key]) && (
                        <FileCode2 className="ml-auto" />
                      )}
                  </Button>
                ))}
              </nav>
              <Button
                variant="outline"
                size="sm"
                className="w-full"
                onClick={() => void changeWorkspace(createService)}
                disabled={disabled || !!problems.length}
              >
                <Plus />
                {zh ? '添加服务' : 'Add service'}
              </Button>
            </div>
            <fieldset
              disabled={disabled || !!problems.length}
              className="min-w-0 space-y-3"
            >
              {service ? (
                <ServiceForm key={`${props.nodeID}:${service}`} context={ctx} tab={serviceTab} onTabChange={value => void changeWorkspace(() => setServiceTab(value))} />
              ) : (
                <div className="space-y-3 py-10" data-testid="project-empty-services">
                  <h3 className="font-medium">{zh ? '先添加第一个服务' : 'Add your first service'}</h3>
                  <p className="max-w-lg text-sm text-muted-foreground">{zh ? '填写服务名称和镜像，再为这个服务配置端口、环境变量、网络和存储。' : 'Start with a name and image, then configure this service’s ports, environment, networks and storage.'}</p>
                  <Button onClick={createService}><Plus />{zh ? '添加第一个服务' : 'Add first service'}</Button>
                </div>
              )}
            </fieldset>
          </div>
          {preview && (
            <aside className="hidden min-w-0 space-y-2 xl:block">
              <Choice
                label={zh ? '预览文件' : 'Preview file'}
                value={file}
                options={[
                  ['compose', 'compose.yml'],
                  ['environment', '.env']
                ]}
                onChange={(value) => setFile(value as typeof file)}
              />
              {sourceEditor(true)}
            </aside>
          )}
        </div>
      )}

    </div>
  )
}

function ConfigGroup({
  context,
  value,
  title,
  fields,
  children
}: {
  context: FormContext
  value: string
  title: ReactNode
  fields: string[]
  children: ReactNode
}) {
  const base = ['services', context.service],
    configured = fields.filter(
      (field) => configValue(context.compose, [...base, field]) !== undefined
    ),
    count = configured.length
  return (
    <TabsContent value={value} className="space-y-4 pt-4">
      <div className="flex flex-wrap items-center gap-2">
        <h4 className="text-sm font-medium">{title}</h4>
        <span className="text-xs text-muted-foreground">{context.zh ? `已配置 ${count} 项` : `${count} configured fields`}</span>
      </div>
      {children}
    </TabsContent>
  )
}
function Field({
  context,
  field,
  label,
  number = false
}: {
  context: FormContext
  field: string
  label?: ReactNode
  number?: boolean
}) {
  const path: ConfigPath = ['services', context.service, ...field.split('.')],
    value = configValue(context.compose, path),
    reason = readonlyReason(context.compose, path)
  const shape =
    value !== undefined && value !== null && typeof value === 'object'
  return (
    <div>
      <ValueInput
        label={label ?? field}
        value={text(value)}
        disabled={!!reason || shape}
        placeholder={context.zh ? '未配置' : 'Not configured'}
        onChange={(next) =>
          context.set(
            path,
            next === ''
              ? REMOVE
              : field === 'image' ? normalizeImage(next) : number && /^\d+(\.\d+)?$/.test(next)
                ? Number(next)
                : next
          )
        }
      />
      {field === 'image' && <p className="mt-1 text-xs text-muted-foreground">{context.zh ? '省略 tag 时使用 latest；digest 与变量表达式保留原文。' : 'Omitted tags use latest; digests and variable expressions keep their source.'}</p>}
      {(reason || shape) && <SourceLink context={context} path={path} />}
    </div>
  )
}
function SourceLink({
  context,
  path
}: {
  context: FormContext
  path: ConfigPath
}) {
  return (
    <Button
      size="sm"
      variant="link"
      className="h-auto px-0 text-xs"
      onClick={() => context.locate(path)}
    >
      <FileCode2 />
      {context.zh
        ? '共享或复杂配置 · 在源码中编辑'
        : 'Shared or complex configuration · edit source'}
    </Button>
  )
}
function BooleanField({
  context,
  field,
  label
}: {
  context: FormContext
  field: string
  label?: ReactNode
}) {
  const path = ['services', context.service, ...field.split('.')],
    value = configValue(context.compose, path)
  return (
    <Choice
      label={label ?? field}
      value={value === undefined ? 'default' : String(value)}
      options={[
        ['default', context.zh ? '未配置' : 'Not configured'],
        ['true', 'true'],
        ['false', 'false']
      ]}
      disabled={
        !!readonlyReason(context.compose, path) ||
        (value !== undefined && typeof value !== 'boolean')
      }
      onChange={(next) =>
        context.set(path, next === 'default' ? REMOVE : next === 'true')
      }
    />
  )
}
function CommandField({
  context,
  field,
  label,
  health = false
}: {
  context: FormContext
  field: string
  label?: ReactNode
  health?: boolean
}) {
  const path = ['services', context.service, ...field.split('.')],
    value = configValue(context.compose, path)
  const mode =
    value === undefined || value === null
      ? 'default'
      : health && Array.isArray(value)
        ? value[0] === 'NONE'
          ? 'empty'
          : value[0] === 'CMD-SHELL'
            ? 'shell'
            : 'list'
        : value === '' || (Array.isArray(value) && !value.length)
          ? 'empty'
          : Array.isArray(value)
            ? 'list'
            : 'string'
  const disabled =
    !!readonlyReason(context.compose, path) ||
    (value !== undefined &&
      value !== null &&
      typeof value !== 'string' &&
      !Array.isArray(value)) ||
    (Array.isArray(value) &&
      (!value.every((item) => typeof item === 'string') ||
        (health && !['NONE', 'CMD', 'CMD-SHELL'].includes(value[0])))) ||
    (health &&
      Array.isArray(value) &&
      value[0] === 'CMD-SHELL' &&
      value.length !== 2)
  return (
    <div className="space-y-2">
      <Choice
        label={label ?? field}
        value={mode}
        disabled={disabled}
        options={[
          ['default', context.zh ? '使用默认' : 'Use default'],
          [
            'empty',
            health ? 'NONE' : context.zh ? '显式清空' : 'Explicit empty'
          ],
          ['string', context.zh ? '字符串' : 'String'],
          ...(health ? [['shell', 'CMD-SHELL'] as [string, string]] : []),
          ['list', health ? 'CMD · argv' : 'argv']
        ]}
        onChange={(next) =>
          context.set(
            path,
            next === 'default'
              ? REMOVE
              : next === 'empty'
                ? health
                  ? ['NONE']
                  : []
                : next === 'list'
                  ? health
                    ? ['CMD']
                    : ['']
                  : next === 'shell'
                    ? ['CMD-SHELL', '']
                    : ' '
          )
        }
      />
      {(mode === 'string' || mode === 'list' || mode === 'shell') && (
        <ValueInput
          label={
            mode === 'list'
              ? context.zh
                ? '每行一个参数'
                : 'One argument per line'
              : label ?? field
          }
          value={
            Array.isArray(value)
              ? (health ? value.slice(1) : value).map(String).join('\n')
              : text(value)
          }
          multiline={mode === 'list'}
          disabled={disabled}
          onChange={(next) =>
            context.set(
              path,
              mode === 'list'
                ? [...(health ? ['CMD'] : []), ...next.split('\n')]
                : mode === 'shell'
                  ? ['CMD-SHELL', next]
                  : next
            )
          }
        />
      )}
      {disabled && <SourceLink context={context} path={path} />}
    </div>
  )
}

function RestartField({ context: c }: { context: FormContext }) {
  const path = ['services', c.service, 'restart'], raw = configValue(c.compose, path)
  const value = text(raw), match = value.match(/^on-failure(?::(\d+))?$/)
  if (readonlyReason(c.compose, path) || (raw !== undefined && !['no', 'always', 'unless-stopped'].includes(value) && !match))
    return <SourceLink context={c} path={path} />
  return <div className="space-y-2">
    <Choice label={c.zh ? '重启策略 restart' : 'Restart policy restart'} value={match ? 'on-failure' : value || '__unset'} options={[
      ['__unset', c.zh ? '未配置 · 默认不重启' : 'Unset · no restart by default'],
      ['no', c.zh ? '不重启 · no' : 'Do not restart · no'],
      ['unless-stopped', c.zh ? '除非手动停止 · unless-stopped' : 'Unless manually stopped · unless-stopped'],
      ['always', c.zh ? '总是重启 · always' : 'Always restart · always'],
      ['on-failure', c.zh ? '失败时重启 · on-failure' : 'Restart on failure · on-failure']
    ]} onChange={next => c.set(path, next === '__unset' ? REMOVE : next === 'on-failure' && match ? value : next)} />
    {match && <ValueInput label={c.zh ? '最大重试次数 · 可选' : 'Maximum retries · optional'} value={match[1] ?? ''} placeholder={c.zh ? '不限制' : 'Unlimited'} onChange={next => {
      if (next && !/^\d+$/.test(next)) { c.fail(c.zh ? '重试次数须为非负整数。' : 'Retries must be a non-negative integer.'); return }
      c.set(path, next ? `on-failure:${next}` : 'on-failure')
    }} />}
  </div>
}

function ServiceNameField({ context: c }: { context: FormContext }) {
  const id = useId(), unnamed = unnamedServiceNumber(c.service) !== undefined
  const value = unnamed ? '' : c.service
  const [name, setName] = useState(value), [error, setError] = useState('')
  const { pending, service } = c
  useEffect(() => setName(value), [value])
  useEffect(() => {
    pending(`service-name:${service}`, name !== value)
    return () => pending(`service-name:${service}`, false)
  }, [pending, service, name, value])
  const commit = () => {
    const next = name.trim()
    if (next === value) { setName(value); setError(''); pending(`service-name:${service}`, false); return }
    if (next && !/^[a-zA-Z0-9][a-zA-Z0-9_.-]*$/.test(next)) {
      setError(c.zh ? '服务名称须以字母或数字开头，只能包含字母、数字、.、_ 和 -。' : 'Start with a letter or number and use only letters, numbers, ., _ and -.')
      return
    }
    if (next !== c.service && configKeys(c.compose, ['services']).includes(next)) {
      setError(c.zh ? '服务名称已存在，请使用其他名称。' : 'This service name already exists. Choose another name.')
      return
    }
    try {
      if (next !== c.service) c.rename(next)
      else setName(value)
      setError('')
      pending(`service-name:${service}`, false)
    } catch (failure) { setError(failure instanceof Error ? failure.message : String(failure)) }
  }
  return <div className="min-w-0 space-y-1.5">
    <Label htmlFor={id}>{c.zh ? '服务名称' : 'Service name'}</Label>
    <Input id={id} name="service-name" value={name} autoComplete="off" autoFocus={unnamed} placeholder={serviceLabel(c.service, c.zh)} aria-invalid={!!error} aria-describedby={`${id}-hint${error ? ` ${id}-error` : ''}`} onChange={event => { setName(event.target.value); setError('') }} onBlur={commit} onKeyDown={event => {
      if (event.key === 'Enter') { event.preventDefault(); commit() }
      if (event.key === 'Escape') { setName(value); setError('') }
    }} />
    <p id={`${id}-hint`} className="text-xs text-muted-foreground">{unnamed ? c.zh ? `可留空，Compose 自动名称为 ${c.service}。` : `Optional. The automatic Compose name is ${c.service}.` : c.zh ? '以字母或数字开头，可包含 .、_ 和 -；留空使用自动名称。' : 'Start with a letter or number; ., _ and - are allowed. Leave blank for an automatic name.'}</p>
    {error && <p id={`${id}-error`} className="text-xs text-destructive" role="alert">{error}</p>}
  </div>
}

function ServiceForm({ context: c, tab, onTabChange }: { context: FormContext; tab: string; onTabChange: (tab: string) => void }) {
  const servicePath = ['services', c.service],
    locked = readonlyReason(c.compose, servicePath)
  const remove = async () => {
    const uses = references(c.compose, 'services', c.service).filter(
      (item) => item !== c.service
    )
    if (uses.length) {
      c.fail(
        c.zh
          ? `请先解除依赖：${uses.map(name => serviceLabel(name, c.zh)).join(', ')}`
          : `Remove references first: ${uses.map(name => serviceLabel(name, c.zh)).join(', ')}`
      )
      return
    }
    if (
      await confirmDialog({
        title: c.zh
          ? `删除服务配置 ${serviceLabel(c.service, c.zh)}？`
          : `Remove service configuration ${serviceLabel(c.service, c.zh)}?`,
        description: c.zh
          ? '仅修改配置，运行态变化发生在下次部署。'
          : 'Changes configuration only. Runtime changes happen on deployment.',
        danger: true
      })
    )
      c.set(servicePath, REMOVE)
  }
  const fields = configKeys(c.compose, servicePath)
  const supported = new Set([
    'image',
    'command',
    'entrypoint',
    'restart',
    'container_name',
    'working_dir',
    'user',
    'ports',
    'expose',
    'environment',
    'volumes',
    'tmpfs',
    'networks',
    'depends_on',
    'healthcheck',
    'cpus',
    'mem_limit',
    'mem_reservation',
    'stop_grace_period',
    'labels',
    'logging',
    'read_only',
    'init',
    'stdin_open',
    'tty'
  ])
  return (
    <div className="space-y-3" data-testid="service-configuration-form">
      <div className="flex items-center justify-between gap-2">
        <h3 className="font-medium">{serviceLabel(c.service, c.zh)}</h3>
        <div className="flex items-center">
          <DeleteButton zh={c.zh} onClick={() => void remove()} />
        </div>
      </div>
      {locked && (
        <Alert>
          <AlertDescription>
            {c.zh
              ? '该服务使用共享 YAML 结构，请在源码中编辑。'
              : 'This service uses shared YAML structures. Edit it in source.'}
            <SourceLink context={c} path={servicePath} />
          </AlertDescription>
        </Alert>
      )}
      <fieldset disabled={!!locked} className="min-w-0">
        <Tabs value={tab} onValueChange={onTabChange} className="gap-0" data-testid="service-configuration-tabs">
          <TabsList variant="line" aria-label={c.zh ? `${serviceLabel(c.service, c.zh)} 服务配置` : `${serviceLabel(c.service, c.zh)} service configuration`} className="group-data-horizontal/tabs:h-auto w-full flex-wrap justify-start border-b pb-1">
            {[
              ['basics', '基础', 'Basics'],
              ['ports', '端口', 'Ports'],
              ['environment', '环境变量', 'Environment'],
              ['networks', '网络与依赖', 'Networks & dependencies'],
              ['storage', '存储', 'Storage'],
              ['health', '健康检查', 'Health checks'],
              ['resources', '资源限制', 'Resources'],
              ['advanced', '高级', 'Advanced']
            ].map(([value, chinese, english]) => <TabsTrigger key={value} value={value} className="h-auto min-h-10 flex-none px-2 py-1.5"><TabCaption zh={c.zh} chinese={chinese} english={english} /></TabsTrigger>)}
          </TabsList>
        <ConfigGroup
          context={c}
          value="basics"
          title={c.zh ? '基础与启动' : 'Basics & startup'}
          fields={[
            'image',
            'command',
            'entrypoint',
            'restart',
            'container_name',
            'working_dir',
            'user'
          ]}
        >
          <div className="grid gap-3 sm:grid-cols-2">
            <ServiceNameField context={c} />
            <Field
              context={c}
              field="image"
              label={c.zh ? '镜像 image' : 'Image image'}
            />
            <RestartField context={c} />
            <Field context={c} field="container_name" />
            <Field context={c} field="working_dir" />
            <Field context={c} field="user" />
          </div>
          <CommandField context={c} field="command" />
          <CommandField context={c} field="entrypoint" />
        </ConfigGroup>
        <ConfigGroup
          context={c}
          value="ports"
          title={c.zh ? '端口' : 'Ports'}
          fields={['ports', 'expose']}
        >
          <Ports context={c} />
          <details open={configValue(c.compose, [...servicePath, 'expose']) !== undefined} className="text-sm">
            <summary className="cursor-pointer py-1 text-muted-foreground">{c.zh ? '服务内部端口 expose · 可选' : 'Internal service ports expose · optional'}</summary>
            <div className="pt-2"><StringList context={c} field="expose" /></div>
          </details>
        </ConfigGroup>
        <ConfigGroup
          context={c}
          value="environment"
          title={c.zh ? '环境变量' : 'Environment'}
          fields={['environment']}
        >
          <div className="flex flex-wrap items-center justify-between gap-2">
            <p className="text-xs text-muted-foreground">{c.zh ? `仅应用于服务 ${c.displayService}。.env 变量须显式引用后才会传入。` : `Applies only to ${c.displayService}. Reference .env variables explicitly to pass them in.`}</p>
          </div>
          <EnvironmentFields context={c} />
          <details className="border-t pt-3 text-sm" data-testid="service-interpolation-variables">
            <summary className="cursor-pointer text-muted-foreground">{c.zh ? '插值变量 .env' : 'Interpolation variables .env'}</summary>
            <div className="pt-3"><InterpolationVariables context={c} /></div>
          </details>
        </ConfigGroup>
        <ConfigGroup
          context={c}
          value="storage"
          title={c.zh ? '存储' : 'Storage'}
          fields={['volumes', 'tmpfs']}
        >
          <Mounts context={c} />
          {configValue(c.compose, [...servicePath, 'tmpfs']) !== undefined && <details open className="text-sm">
            <summary className="cursor-pointer py-1 text-muted-foreground">{c.zh ? '其他临时挂载 tmpfs' : 'Additional temporary mounts tmpfs'}</summary>
            <div className="pt-2"><StringList context={c} field="tmpfs" /></div>
          </details>}
        </ConfigGroup>
        <ConfigGroup
          context={c}
          value="networks"
          title={c.zh ? '网络与依赖' : 'Networks & dependencies'}
          fields={['networks', 'depends_on']}
        >
          <ServiceNetworks context={c} />
          <Dependencies context={c} />
        </ConfigGroup>
        <ConfigGroup context={c} value="health" title={<FieldCaption zh={c.zh} chinese="健康检查" english="Health checks" field="healthcheck" />} fields={['healthcheck']}>
          <CommandField context={c} field="healthcheck.test" health label={<FieldCaption zh={c.zh} chinese="检查命令" english="Check command" field="healthcheck.test" />} />
          <BooleanField context={c} field="healthcheck.disable" label={<FieldCaption zh={c.zh} chinese="禁用健康检查" english="Disable health check" field="healthcheck.disable" />} />
          <div className="grid gap-3 sm:grid-cols-2">
            {[
              ['healthcheck.interval', '检查间隔', 'Check interval'],
              ['healthcheck.timeout', '超时时间', 'Timeout'],
              ['healthcheck.retries', '失败重试次数', 'Retries'],
              ['healthcheck.start_period', '启动等待', 'Start period'],
              ['healthcheck.start_interval', '启动期间检查间隔', 'Start interval']
            ].map(([field, chinese, english]) => <Field key={field} context={c} field={field} number={field.endsWith('retries')} label={<FieldCaption zh={c.zh} chinese={chinese} english={english} field={field} />} />)}
          </div>
        </ConfigGroup>
        <ConfigGroup context={c} value="resources" title={<TabCaption zh={c.zh} chinese="资源限制" english="Resources" />} fields={['cpus', 'mem_limit', 'mem_reservation', 'stop_grace_period']}>
          <div className="grid gap-3 sm:grid-cols-2">
            {[
              ['cpus', 'CPU 限额', 'CPU limit'],
              ['mem_limit', '内存上限', 'Memory limit'],
              ['mem_reservation', '内存预留', 'Memory reservation'],
              ['stop_grace_period', '停止等待时间', 'Stop grace period']
            ].map(([field, chinese, english]) => <Field key={field} context={c} field={field} number={field === 'cpus'} label={<FieldCaption zh={c.zh} chinese={chinese} english={english} field={field} />} />)}
          </div>
          {fields.includes('deploy') && <SourceLink context={c} path={[...servicePath, 'deploy']} />}
        </ConfigGroup>
        <ConfigGroup
          context={c}
          value="advanced"
          title={c.zh ? '高级设置' : 'Advanced'}
          fields={[
            'labels',
            'logging',
            'read_only',
            'init',
            'stdin_open',
            'tty'
          ]}
        >
          <MapFields context={c} path={[...servicePath, 'labels']} />
          <section className="space-y-3 border-t pt-4" data-testid="service-logging">
            <h5 className="text-sm font-medium"><FieldCaption zh={c.zh} chinese="日志" english="Logging" field="logging" /></h5>
            <Field context={c} field="logging.driver" label={<FieldCaption zh={c.zh} chinese="日志驱动" english="Log driver" field="logging.driver" />} />
            <details open={configValue(c.compose, [...servicePath, 'logging', 'options']) !== undefined}>
              <summary className="cursor-pointer text-sm text-muted-foreground">{c.zh ? '日志驱动选项' : 'Log driver options'}</summary>
          <MapFields
            context={c}
            path={[...servicePath, 'logging', 'options']}
          />
            </details>
          </section>
          <div className="grid gap-3 sm:grid-cols-2">
            {[['read_only', '只读根文件系统', 'Read-only root filesystem'], ['init', '初始化进程', 'Init process'], ['stdin_open', '保持标准输入', 'Keep standard input open'], ['tty', '分配终端', 'Allocate a TTY']].map(([field, chinese, english]) => (
              <BooleanField key={field} context={c} field={field} label={<FieldCaption zh={c.zh} chinese={chinese} english={english} field={field} />} />
            ))}
          </div>
        </ConfigGroup>
        </Tabs>
      </fieldset>
      {!!fields.filter((field) => !supported.has(field)).length && (
        <div className="rounded-md border p-3 text-xs text-muted-foreground">
          {c.zh ? '保留的源码配置：' : 'Preserved source configuration: '}
          {fields
            .filter((field) => !supported.has(field))
            .map((field) => (
              <Button
                key={field}
                variant="link"
                size="sm"
                onClick={() => c.locate([...servicePath, field])}
              >
                {field}
              </Button>
            ))}
        </div>
      )}
    </div>
  )
}

function StringList({
  context: c,
  field
}: {
  context: FormContext
  field: string
}) {
  const path = ['services', c.service, field],
    value = configValue(c.compose, path),
    safe =
      value === undefined ||
      (Array.isArray(value) &&
        value.every(
          (item) => typeof item === 'string' || typeof item === 'number'
        ))
  return (
    <>
      <ValueInput
        label={`${field} · ${c.zh ? '每行一项' : 'one per line'}`}
        value={
          Array.isArray(value) ? value.map(String).join('\n') : text(value)
        }
        disabled={!safe || !!readonlyReason(c.compose, path)}
        multiline
        onChange={(next) =>
          c.set(path, next ? next.split('\n').filter(Boolean) : REMOVE)
        }
      />
      {!safe && <SourceLink context={c} path={path} />}
    </>
  )
}
function MapFields({ context: c, path }: { context: FormContext; path: ConfigPath }) {
  const labels = path.at(-1) === 'labels'
  const id = useId(), keyInput = useRef<HTMLInputElement>(null)
  const [adding, setAdding] = useState(false), [key, setKey] = useState(''), [draft, setDraft] = useState(''), [error, setError] = useState(''), [reveal, setReveal] = useState(false)
  const { pending, service } = c, mapPath = path.join('.')
  useEffect(() => {
    const pendingKey = `map:${service}:${mapPath}`
    pending(pendingKey, adding && (!!key || !!draft))
    return () => pending(pendingKey, false)
  }, [pending, service, mapPath, adding, key, draft])
  const value = configValue(c.compose, path)
  const list = Array.isArray(value) && labels && value.every(item => typeof item === 'string') ? value as string[] : undefined
  const entries = list?.map((item): [string, string] => {
    const index = item.indexOf('=')
    return index < 0 ? [item, ''] : [item.slice(0, index), item.slice(index + 1)]
  })
  const map = entries ? Object.fromEntries(entries) : record(value)
  const complex = value !== undefined && (!value || typeof value !== 'object' || (Array.isArray(value) && !list) || (entries && Object.keys(map).length !== entries.length))
  const changed = (key: string, next: string | typeof REMOVE) => {
    if (!list || !entries) return editConfig(c.compose, [...path, key], next)
    const index = entries.findIndex(([name]) => name === key)
    return index < 0 && next !== REMOVE ? editConfig(c.compose, path, [...list, `${key}=${next}`]) : editConfig(c.compose, [...path, index], next === REMOVE ? REMOVE : `${key}=${next}`)
  }
  const update = (key: string, next: string | typeof REMOVE) => {
    try { c.onCompose(changed(key, next)) } catch (error) { c.fail(error) }
  }
  const addLabel = labels ? c.zh ? '添加标签' : 'Add label' : c.zh ? '添加日志选项' : 'Add log option'
  return <section className="space-y-3" data-testid={labels ? 'service-labels' : 'service-log-options'}>
    <div className="flex items-center gap-2"><h5 className="text-sm font-medium"><FieldCaption zh={c.zh} chinese={labels ? '标签' : '日志选项'} english={labels ? 'Labels' : 'Log options'} field={labels ? 'labels' : 'logging.options'} /></h5><span className="text-xs text-muted-foreground">{Object.keys(map).length}</span></div>
    <p className="text-xs text-muted-foreground">{labels ? c.zh ? '容器元数据，以键和值填写，例如 com.example.role = api。' : 'Container metadata as key/value pairs, such as com.example.role = api.' : c.zh ? '传给所选日志驱动的选项，例如 max-size = 10m。' : 'Options passed to the selected log driver, such as max-size = 10m.'}</p>
    {complex || readonlyGroupReason(c.compose, path) ? <SourceLink context={c} path={path} /> : <>
      {Object.entries(map).map(([key, item]) => <div key={key} className="flex items-end gap-2 border-b pb-3"><div className="min-w-0 flex-1"><ValueInput label={key} value={text(item)} secret={sensitiveKey(key)} disabled={!!readonlyReason(c.compose, [...path, key]) || (item !== null && typeof item === 'object')} onChange={next => update(key, next)} /></div><DeleteButton zh={c.zh} onClick={() => update(key, REMOVE)} /></div>)}
      {adding ? <form className="space-y-3" data-testid={labels ? 'new-label-form' : 'new-log-option-form'} onSubmit={event => {
        event.preventDefault()
        const name = key.trim()
        if (!name || /[=\r\n]/.test(name)) { setError(c.zh ? '请填写键名，不包含等号或换行。' : 'Enter a key without equals signs or newlines.'); return }
        if (Object.hasOwn(map, name)) { setError(c.zh ? '键名已存在，请编辑现有值。' : 'This key already exists. Edit its current value.'); return }
        try { c.onCompose(changed(name, draft)); setKey(''); setDraft(''); setError(''); setReveal(false); keyInput.current?.focus() }
        catch (failure) { setError(failure instanceof Error ? failure.message : String(failure)) }
      }}>
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="flex min-w-0 flex-col gap-1.5"><Label htmlFor={`${id}-key`}>{labels ? c.zh ? '标签键 key' : 'Label key' : c.zh ? '选项键 key' : 'Option key'}</Label><Input ref={keyInput} autoFocus id={`${id}-key`} value={key} autoComplete="off" placeholder={labels ? 'com.example.role' : 'max-size'} onChange={event => { setKey(event.target.value); setError('') }} /></div>
          <div className="flex min-w-0 flex-col gap-1.5"><Label htmlFor={`${id}-value`}>{labels ? c.zh ? '标签值 value' : 'Label value' : c.zh ? '选项值 value' : 'Option value'}</Label><div className="flex items-center gap-1"><Input id={`${id}-value`} type={sensitiveKey(key) && !reveal ? 'password' : 'text'} value={draft} autoComplete="off" placeholder={labels ? 'api' : '10m'} onChange={event => { setDraft(event.target.value); setError('') }} />{sensitiveKey(key) && <Button type="button" size="icon-sm" variant="ghost" aria-label={c.zh ? '切换敏感值显示' : 'Toggle sensitive value'} onClick={() => setReveal(!reveal)}>{reveal ? <EyeOff /> : <Eye />}</Button>}</div></div>
        </div>
        {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
        <div className="flex gap-2"><Button type="submit" size="sm">{addLabel}</Button><Button type="button" size="sm" variant="ghost" onClick={() => { setAdding(false); setKey(''); setDraft(''); setError('') }}>{c.zh ? '取消' : 'Cancel'}</Button></div>
      </form> : <Button variant="outline" size="sm" onClick={() => setAdding(true)}><Plus />{addLabel}</Button>}
    </>}
  </section>
}

function Ports({ context: c }: { context: FormContext }) {
  const path = ['services', c.service, 'ports'], raw = configValue(c.compose, path)
  if (readonlyGroupReason(c.compose, path) || (raw !== undefined && !Array.isArray(raw))) return <SourceLink context={c} path={path} />
  return <PortMappings context={c} sourceLink={index => <SourceLink context={c} path={[...path, index]} />} />
}
function parseMount(value: unknown): Record<string, unknown> | undefined {
  if (value && typeof value === 'object' && !Array.isArray(value))
    return value as Record<string, unknown>
  if (typeof value !== 'string' || value.includes('$')) return undefined
  const parts = value.split(':')
  if (parts.length === 1) return { type: 'volume', target: parts[0] }
  if (parts.length > 3 || (parts[2] && !['ro', 'rw'].includes(parts[2])))
    return undefined
  return {
    type: /^(\/|\.)/.test(parts[0]) ? 'bind' : 'volume',
    source: parts[0],
    target: parts[1],
    ...(parts[2] ? { read_only: parts[2] === 'ro' } : {})
  }
}
function Mounts({ context: c }: { context: FormContext }) {
  const path = ['services', c.service, 'volumes'], raw = configValue(c.compose, path)
  const [adding, setAdding] = useState(!Array.isArray(raw) || !raw.length)
  if (readonlyGroupReason(c.compose, path) || (raw !== undefined && !Array.isArray(raw))) return <SourceLink context={c} path={path} />
  const rows = Array.isArray(raw) ? raw : []
  return <div className="space-y-2">
    <p className="text-sm font-medium">{c.zh ? '来源 → 容器内路径' : 'Source → container path'}</p>
    <p className="text-xs text-muted-foreground">{c.zh ? `命名卷由 Docker 管理；主机路径使用节点 ${c.nodeID} 上的目录或文件。` : `Named volumes are managed by Docker; host paths use directories or files on node ${c.nodeID}.`}</p>
    {rows.map((row, index) => {
      const mount = parseMount(row)
      if (!mount || !['volume', 'bind', 'tmpfs'].includes(text(mount.type)) || readonlyReason(c.compose, [...path, index]) || ['source', 'target', 'read_only'].some(key => mount[key] != null && typeof mount[key] === 'object')) return <div key={index} className="border-b py-3"><SourceLink context={c} path={[...path, index]} /></div>
      return <MountForm key={index} context={c} mount={mount} index={index} />
    })}
    {adding && <MountForm context={c} onClose={() => setAdding(false)} />}
    {!adding && <Button variant="outline" size="sm" onClick={() => setAdding(true)}><Plus />{c.zh ? '添加存储映射' : 'Add storage mapping'}</Button>}
  </div>
}

function EnvironmentFields({ context: c }: { context: FormContext }) {
  const path = ['services', c.service, 'environment'],
    raw = configValue(c.compose, path)
  const [bulk, setBulk] = useState(false),
    [paste, setPaste] = useState(''),
    [adding, setAdding] = useState(false),
    [referenceKey, setReferenceKey] = useState<string | null>(null)
  if (
    readonlyGroupReason(c.compose, path) ||
    (raw !== undefined &&
      !Array.isArray(raw) &&
      (!raw || typeof raw !== 'object'))
  )
    return <SourceLink context={c} path={path} />
  const entries: [string, unknown][] = Array.isArray(raw)
    ? raw.map((item) => {
        const value = text(item),
          index = value.indexOf('=')
        return index < 0
          ? [value, null]
          : [value.slice(0, index), value.slice(index + 1)]
      })
    : Object.entries(record(raw))
  const vars = envEntries(c.environment)
  const update = (key: string, value: unknown | typeof REMOVE) => {
    if (Array.isArray(raw)) {
      const next = [...raw],
        index = entries.findIndex(([name]) => name === key)
      if (value === REMOVE) next.splice(index, 1)
      else if (index < 0)
        next.push(value === null ? key : `${key}=${text(value)}`)
      else next[index] = value === null ? key : `${key}=${text(value)}`
      c.set(path, next)
    } else c.set([...path, key], value)
  }
  const create = async (key: string, value: string) => {
    const name = await promptDialog({
      title: c.zh ? '创建 .env 变量' : 'Create .env variable',
      input: { label: 'KEY', initialValue: key }
    })
    if (!name) return
    try {
      if (vars.some((item) => item.key === name))
        throw new Error(
          c.zh
            ? '.env 变量已存在，请从列表选择'
            : 'Variable already exists; select it from the list'
        )
      c.onEnvironment(setEnv(c.environment, name, value))
      update(key, '${' + name + '}')
    } catch (error) {
      c.fail(error)
    }
  }
  const applyPaste = () => {
    try {
      const parsed = envEntries(paste)
      if (
        !parsed.length ||
        parsed.some((item) => !item.editable) ||
        parsed.length !==
          paste
            .split('\n')
            .filter((line) => line.trim() && !line.trim().startsWith('#'))
            .length
      )
        throw new Error(
          c.zh
            ? '使用唯一的 KEY=value 行，复杂表达式请在源码中填写'
            : 'Use unique KEY=value lines; edit complex expressions in source'
        )
      if (parsed.some((item) => entries.some(([key]) => key === item.key)))
        throw new Error(
          c.zh
            ? '存在重复变量，请先处理冲突'
            : 'Duplicate variables; resolve conflicts first'
        )
      const next = Array.isArray(raw)
        ? [...raw, ...parsed.map((item) => `${item.key}=${item.value}`)]
        : {
            ...record(raw),
            ...Object.fromEntries(parsed.map((item) => [item.key, item.value]))
          }
      c.set(path, next)
      setBulk(false)
      setPaste('')
    } catch (error) {
      c.fail(error)
    }
  }
  return (
    <div className="space-y-3">
      {entries.map(([key, value], i) => {
        const duplicate = entries.filter(([name]) => name === key).length > 1
        const pathKey = Array.isArray(raw) ? i : key
        const complex =
          !!readonlyReason(c.compose, [...path, pathKey]) ||
          (value !== null && typeof value === 'object')
        const ref =
          typeof value === 'string'
            ? value.match(/^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$/)?.[1]
            : undefined
        const mode = value == null ? 'inherit' : ref ? 'reference' : 'literal'
        return (
          <div key={`${key}-${i}`} className="space-y-1">
            <fieldset
              disabled={duplicate || complex}
              className="space-y-2 rounded-md border p-2"
            >
              <div className="flex items-center justify-between">
                <span className="text-sm font-medium">{key}</span>
                <DeleteButton zh={c.zh} onClick={() => update(key, REMOVE)} />
              </div>
              <div className="grid gap-2 sm:grid-cols-2">
                <Choice
                  label={c.zh ? '值来源' : 'Value source'}
                  value={mode}
                  options={[
                    [
                      'literal',
                      c.zh ? '直接值 / 表达式' : 'Direct value / expression'
                    ],
                    ['reference', c.zh ? '.env 变量' : '.env variable'],
                    [
                      'inherit',
                      c.zh ? '继承环境值' : 'Inherit environment value'
                    ]
                  ]}
                  onChange={(next) => {
                    if (next === 'inherit') update(key, null)
                    else if (next === 'literal') update(key, '')
                    else if (vars.length) setReferenceKey(key)
                    else void create(key, text(value))
                  }}
                />
                {mode === 'reference' ? (
                  <Choice
                    label={c.zh ? '.env 变量' : '.env variable'}
                    value={ref!}
                    options={[
                      ...vars.map((item): [string, string] => [
                        item.key,
                        item.key
                      ]),
                      ...(!vars.some((item) => item.key === ref)
                        ? [[ref!, `${ref} · missing`] as [string, string]]
                        : [])
                    ]}
                    onChange={(next) => update(key, '${' + next + '}')}
                  />
                ) : mode === 'literal' ? (
                  <ValueInput
                    label="value"
                    value={text(value)}
                    secret={sensitiveKey(key)}
                    onChange={(next) => update(key, next)}
                  />
                ) : null}
              </div>
              {mode === 'literal' && (
                <Button
                  variant="link"
                  size="sm"
                  className="px-0"
                  onClick={() => void create(key, text(value))}
                >
                  {c.zh
                    ? '创建并引用.env 变量'
                    : 'Create and reference .env variable'}
                </Button>
              )}
            </fieldset>
            {(duplicate || complex) && (
              <SourceLink context={c} path={[...path, pathKey]} />
            )}
          </div>
        )
      })}
      <div className="flex flex-wrap gap-2">
        <Button variant="outline" size="sm" onClick={() => setAdding(true)}>
          <Plus />
          {c.zh ? '添加变量' : 'Add variable'}
        </Button>
        <Button variant="ghost" size="sm" onClick={() => setBulk(true)}>
          {c.zh ? '批量粘贴' : 'Paste variables'}
        </Button>
      </div>
      {adding && <EnvironmentVariableDialog context={c} keys={entries.map(([key]) => key)} onAdd={update} onClose={() => setAdding(false)} />}
      {referenceKey !== null && <ProjectVariableReferenceDialog context={c} variableKey={referenceKey} onSelect={name => update(referenceKey, '${' + name + '}')} onClose={() => setReferenceKey(null)} />}
      <Dialog open={bulk} onOpenChange={setBulk}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {c.zh ? '批量粘贴环境变量' : 'Paste environment variables'}
            </DialogTitle>
          </DialogHeader>
          <p className="text-xs text-muted-foreground">
            {c.zh
              ? '每行 KEY=value；此输入区用于录入原文。'
              : 'One KEY=value per line. This input accepts source text.'}
          </p>
          <Textarea
            aria-label="KEY=value"
            value={paste}
            onChange={(event) => setPaste(event.target.value)}
            rows={8}
          />
          <DialogFooter>
            <Button onClick={applyPaste}>{c.zh ? '添加' : 'Add'}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}

function ProjectVariableReferenceDialog({ context: c, variableKey, onSelect, onClose }: {
  context: FormContext
  variableKey: string
  onSelect: (name: string) => void
  onClose: () => void
}) {
  const [selected, setSelected] = useState('')
  const variables = envEntries(c.environment)
  return <Dialog open onOpenChange={open => { if (!open) onClose() }}>
    <DialogContent>
      <DialogHeader><DialogTitle>{c.zh ? '选择 .env 变量' : 'Select .env variable'}</DialogTitle><DialogDescription>{c.displayService} · {variableKey}</DialogDescription></DialogHeader>
      <form className="space-y-4" onSubmit={event => {
        event.preventDefault()
        if (c.disabled || !variables.some(variable => variable.key === selected)) return
        onSelect(selected)
        onClose()
      }}>
        <Choice label={c.zh ? '.env 变量' : '.env variable'} value={selected || '__select'} options={[
          ['__select', c.zh ? '请选择变量' : 'Select a variable'],
          ...variables.map((variable): [string, string] => [variable.key, variable.key])
        ]} onChange={name => setSelected(name === '__select' ? '' : name)} />
        <DialogFooter><Button type="button" variant="outline" onClick={onClose}>{c.zh ? '取消' : 'Cancel'}</Button><Button type="submit" disabled={c.disabled || !selected}>{c.zh ? '引用变量' : 'Reference variable'}</Button></DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
}

function EnvironmentVariableDialog({ context: c, keys, onAdd, onClose }: {
  context: FormContext
  keys: string[]
  onAdd: (key: string, value: unknown) => void
  onClose: () => void
}) {
  const id = useId()
  const [key, setKey] = useState(''), [value, setValue] = useState(''),
    [mode, setMode] = useState('literal'), [reference, setReference] = useState(''),
    [reveal, setReveal] = useState(false), [error, setError] = useState('')
  const variables = envEntries(c.environment)
  return <Dialog open onOpenChange={open => { if (!open) onClose() }}>
    <DialogContent className="sm:max-w-lg">
      <DialogHeader><DialogTitle>{c.zh ? '添加服务环境变量' : 'Add service environment variable'}</DialogTitle><DialogDescription>{c.zh ? `仅配置服务 ${c.displayService}，不会自动修改其他服务。` : `Configure ${c.displayService} independently.`}</DialogDescription></DialogHeader>
      <form className="space-y-4" onSubmit={event => {
        event.preventDefault()
        if (c.disabled) return
        const name = key.trim()
        if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name) || keys.includes(name)) {
          setError(c.zh ? '变量名无效或在当前服务中已存在。' : 'Invalid variable name or already present in this service.')
          return
        }
        if (mode === 'reference' && !variables.some(variable => variable.key === reference)) {
          setError(c.zh ? '请选择 .env 变量。' : 'Select a .env variable.')
          return
        }
        onAdd(name, mode === 'reference' ? '${' + reference + '}' : mode === 'inherit' ? null : value)
        onClose()
      }}>
        <div className="space-y-1.5"><Label htmlFor={`${id}-key`}>{c.zh ? '变量名 KEY' : 'Variable name KEY'}</Label><Input id={`${id}-key`} autoFocus autoComplete="off" value={key} onChange={event => { setKey(event.target.value); setError('') }} placeholder="APP_MODE" /></div>
        <Choice label={c.zh ? '值来源' : 'Value source'} value={mode} options={[
          ['literal', c.zh ? '直接值 / 表达式' : 'Direct value / expression'],
          ['reference', c.zh ? '.env 变量' : '.env variable'],
          ['inherit', c.zh ? '继承环境值' : 'Inherit environment value']
        ]} onChange={next => { setMode(next); setError('') }} />
        {mode === 'reference' ? <Choice label={c.zh ? '.env 变量' : '.env variable'} value={reference || '__select'} options={[
          ['__select', c.zh ? '请选择变量' : 'Select a variable'],
          ...variables.map((variable): [string, string] => [variable.key, variable.key])
        ]} onChange={next => { setReference(next === '__select' ? '' : next); setError('') }} /> : mode === 'literal' ? <div className="space-y-1.5"><Label htmlFor={`${id}-value`}>{c.zh ? '值 value' : 'Value value'}</Label><div className="flex gap-2"><Input id={`${id}-value`} autoComplete="off" type={sensitiveKey(key) && !reveal ? 'password' : 'text'} value={value} onChange={event => setValue(event.target.value)} />{sensitiveKey(key) && <Button type="button" size="icon" variant="ghost" aria-label={c.zh ? '切换敏感值显示' : 'Toggle sensitive value visibility'} onClick={() => setReveal(!reveal)}>{reveal ? <EyeOff /> : <Eye />}</Button>}</div></div> : <p className="text-xs text-muted-foreground">{c.zh ? '保留无值声明，由 Compose 运行环境解析。' : 'Keep a valueless declaration, resolved from the Compose process environment.'}</p>}
        {error && <Alert variant="destructive"><AlertDescription>{error}</AlertDescription></Alert>}
        <DialogFooter><Button type="button" variant="outline" onClick={onClose}>{c.zh ? '取消' : 'Cancel'}</Button><Button type="submit" disabled={c.disabled}>{c.zh ? '添加到服务' : 'Add to service'}</Button></DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
}

function ServiceNetworks({ context: c }: { context: FormContext }) {
  const [adding, setAdding] = useState(false)
  const path = ['services', c.service, 'networks'], raw = configValue(c.compose, path)
  if (configValue(c.compose, ['services', c.service, 'network_mode']) !== undefined)
    return <div className="space-y-2"><p className="text-sm text-muted-foreground">{c.zh ? '此服务使用 network_mode，请在源码中调整网络模式。' : 'This service uses network_mode. Edit its network mode in source.'}</p><SourceLink context={c} path={['services', c.service, 'network_mode']} /></div>
  if (readonlyGroupReason(c.compose, path) || (raw != null && (typeof raw !== 'object' || (Array.isArray(raw) && raw.some(item => typeof item !== 'string')))))
    return <SourceLink context={c} path={path} />
  const entries: [string, unknown][] = Array.isArray(raw) ? raw.map(item => [String(item), null]) : Object.entries(record(raw))
  const remove = (name: string) => {
    if (entries.length === 1) c.set(path, REMOVE)
    else if (Array.isArray(raw)) c.set(path, raw.filter(item => item !== name))
    else c.set([...path, name], REMOVE)
  }
  return <div className="space-y-3">
    <p className="text-xs text-muted-foreground">{c.zh ? `只有服务 ${c.displayService} 会连接下列网络。` : `Only ${c.displayService} is connected to the networks selected here.`}</p>
    {!entries.length && <div className="border-b py-3"><p className="text-sm">{c.zh ? '默认网络' : 'Default network'}</p><p className="mt-1 text-xs text-muted-foreground">{c.zh ? '沿用 Compose 默认连接，部署时创建或复用。' : 'Use Compose’s default connection; created or reused on deployment.'}</p></div>}
    {entries.map(([name, value]) => {
      const resource = record(configValue(c.compose, ['networks', name]))
      const aliases = record(value).aliases
      const complex = (value != null && (typeof value !== 'object' || Array.isArray(value))) ||
        (aliases !== undefined && (!Array.isArray(aliases) || aliases.some(alias => typeof alias !== 'string')))
      if (complex) return <div key={name} className="border-b py-3"><p className="mb-2 text-sm">{name}</p><SourceLink context={c} path={[...path, name]} /></div>
      return <div key={name} className="space-y-3 border-b py-3">
        <div className="flex items-center justify-between gap-2"><div className="min-w-0"><p className="break-all text-sm">{name === 'default' ? (c.zh ? '默认网络' : 'Default network') : text(resource.name) || name}</p><p className="mt-1 text-xs text-muted-foreground">{resource.external ? (c.zh ? '引用节点已有网络' : 'Existing node network') : (c.zh ? '部署时创建' : 'Created on deployment')}{resource.name ? ` · ${name}` : ''}</p></div>{!(name === 'default' && entries.length === 1) && <TooltipHint content={c.zh ? '解除当前服务连接' : 'Disconnect this service'}><Button variant="ghost" size="icon-sm" aria-label={c.zh ? `解除网络 ${name}` : `Disconnect network ${name}`} onClick={() => remove(name)}><X /></Button></TooltipHint>}</div>
        <ValueInput label={c.zh ? `服务别名 ${name} · 每行一个` : `Service aliases ${name} · one per line`} value={Array.isArray(record(value).aliases) ? (record(value).aliases as unknown[]).map(String).join('\n') : ''} multiline onChange={aliases => {
          if (!Array.isArray(raw)) c.set([...path, name, 'aliases'], aliases ? aliases.split('\n').filter(Boolean) : REMOVE)
          else {
            const next = Object.fromEntries(entries)
            next[name] = aliases ? { aliases: aliases.split('\n').filter(Boolean) } : null
            c.set(path, next)
          }
        }} />
      </div>
    })}
    <p className="text-xs text-muted-foreground">{c.zh ? '未显式选择网络时，服务会使用默认网络。' : 'Without explicit networks, the service uses the project default network.'}</p>
    <Button variant="outline" size="sm" onClick={() => setAdding(true)}><Plus />{c.zh ? '添加网络' : 'Add network'}</Button>
    {adding && <AddNetworkDialog key={c.service} context={c} onClose={() => setAdding(false)} />}
  </div>
}

function Dependencies({ context: c }: { context: FormContext }) {
  const [select, setSelect] = useState('')
  const path = ['services', c.service, 'depends_on'], raw = configValue(c.compose, path)
  if (readonlyGroupReason(c.compose, path) || (raw != null && (typeof raw !== 'object' || (Array.isArray(raw) && raw.some(item => typeof item !== 'string'))))) return <SourceLink context={c} path={path} />
  const entries: [string, unknown][] = Array.isArray(raw) ? raw.map(item => [text(item), { condition: 'service_started' }]) : Object.entries(record(raw))
  const update = (name: string, value: unknown | typeof REMOVE) => {
    if (!Array.isArray(raw)) c.set([...path, name], value)
    else {
      const next = Object.fromEntries(entries)
      if (value === REMOVE) delete next[name]
      else next[name] = value
      c.set(path, next)
    }
  }
  const choices = configKeys(c.compose, ['services']).filter(name => name !== c.service && !entries.some(([key]) => key === name))
  return <div className="space-y-3 border-t pt-4"><h4 className="text-sm font-medium">{c.zh ? '启动依赖 depends_on' : 'Startup dependencies depends_on'}</h4>
    {entries.map(([name, value]) => <div key={name} className="space-y-2"><div className="flex items-center justify-between"><span className="text-sm">{serviceLabel(name, c.zh)}</span><DeleteButton zh={c.zh} onClick={() => update(name, REMOVE)} /></div><Choice label="condition" value={text(record(value).condition) || 'service_started'} options={['service_started', 'service_healthy', 'service_completed_successfully'].map(condition => [condition, condition])} onChange={condition => update(name, { ...record(value), condition })} /></div>)}
    <div className="flex items-end gap-2"><div className="min-w-0 flex-1"><Choice label={c.zh ? '依赖服务' : 'Dependency service'} value={select || '__select'} options={[[ '__select', c.zh ? '选择服务' : 'Select service'], ...choices.map((name): [string,string] => [name,serviceLabel(name, c.zh)])]} onChange={name => setSelect(name === '__select' ? '' : name)} /></div><Button variant="outline" size="sm" disabled={!choices.includes(select)} onClick={() => { update(select, { condition: 'service_started' }); setSelect('') }}><Plus />{c.zh ? '添加依赖' : 'Add dependency'}</Button></div>
  </div>
}

function InterpolationVariables({ context: c }: { context: FormContext }) {
  const rows = envEntries(c.environment)
  const set = (key: string, value: string | typeof REMOVE) => {
    try {
      c.onEnvironment(setEnv(c.environment, key, value))
    } catch (error) {
      c.fail(error)
    }
  }
  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h3 className="font-medium">
          {c.zh ? '插值变量' : 'Interpolation variables'}{' '}
          <span className="text-muted-foreground">.env</span>
        </h3>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => c.locate([], 'environment')}
        >
          <FileCode2 />
          {c.zh ? '编辑源码' : 'Edit source'}
        </Button>
      </div>
      <p className="text-xs text-muted-foreground">
        {c.zh
          ? '用于 Compose 插值；服务通过显式引用注入容器环境。'
          : 'Used for Compose interpolation; services inject values through explicit references.'}
      </p>
      {rows.map((row, i) => {
        const uses = variableReferences(c.compose, row.key)
        return (
          <div key={`${row.key}-${i}`} className="rounded-md border p-3">
            <div className="flex items-end gap-2">
              <div className="min-w-0 flex-1">
                <ValueInput
                  label={row.key}
                  value={
                    row.editable
                      ? row.value
                      : sensitiveKey(row.key)
                        ? '••••••••'
                        : row.raw
                  }
                  secret={sensitiveKey(row.key)}
                  disabled={!row.editable}
                  onChange={(value) => set(row.key, value)}
                />
              </div>
              <DeleteButton
                zh={c.zh}
                onClick={() =>
                  uses.length
                    ? c.fail(
                        c.zh
                          ? `仍被引用：${uses.map(name => serviceLabel(name, c.zh)).join(', ')}`
                          : `Still referenced: ${uses.map(name => serviceLabel(name, c.zh)).join(', ')}`
                      )
                    : set(row.key, REMOVE)
                }
              />
            </div>
            <p className="mt-2 text-xs text-muted-foreground">
              {c.zh ? '引用服务：' : 'Referenced by: '}
              {uses.map(name => serviceLabel(name, c.zh)).join(', ') || '—'}
            </p>
            {!row.editable && (
              <Button
                variant="link"
                size="sm"
                onClick={() => c.locate([], 'environment')}
              >
                {c.zh
                  ? '复杂赋值 · 在源码中编辑'
                  : 'Complex assignment · edit source'}
              </Button>
            )}
          </div>
        )
      })}
    </div>
  )
}

const Diff = lazy(() =>
  import('../../lib/monaco-editor').then((module) => ({
    default: module.MonacoDiffEditor
  }))
)
export function ConfigurationDiff({
  beforeCompose,
  beforeEnvironment,
  compose,
  environment,
  zh
}: {
  beforeCompose: string
  beforeEnvironment: string
  compose: string
  environment: string
  zh: boolean
}) {
  const [file, setFile] = useState('compose'),
    [reveal, setReveal] = useState(false),
    dark = useUIStore((state) => state.resolvedDark)
  const before = file === 'compose' ? beforeCompose : beforeEnvironment,
    after = file === 'compose' ? compose : environment
  const mask = file === 'compose' ? maskCompose : maskEnvironment
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center justify-between">
        <Tabs value={file} onValueChange={setFile}>
          <TabsList>
            <TabsTrigger value="compose">compose.yml</TabsTrigger>
            <TabsTrigger value="environment">.env</TabsTrigger>
          </TabsList>
        </Tabs>
        <Button variant="ghost" size="sm" onClick={() => setReveal(!reveal)}>
          {reveal ? <EyeOff /> : <Eye />}
          {reveal
            ? zh
              ? '隐藏敏感值'
              : 'Hide secrets'
            : zh
              ? '显示敏感值'
              : 'Reveal secrets'}
        </Button>
      </div>
      <div className="h-72 min-w-0 overflow-hidden rounded-md border">
        <Suspense fallback={<Spinner />}>
          <Diff
            original={reveal ? before : mask(before)}
            modified={reveal ? after : mask(after)}
            language={file === 'compose' ? 'yaml' : 'ini'}
            theme={dark ? 'vs-dark' : 'light'}
          />
        </Suspense>
      </div>
    </div>
  )
}

export function ConfigurationOverview({
  compose,
  before = '',
  zh,
  onLocate
}: {
  compose: string
  before?: string
  zh: boolean
  onLocate?: (path: ConfigPath) => void
}) {
  const summaries = useMemo(
    () =>
      configKeys(compose, ['services']).map((service) => {
        const path = ['services', service],
          current = record(configValue(compose, path)),
          previous = record(configValue(before, path))
        const changed = [
          ...new Set([...Object.keys(current), ...Object.keys(previous)])
        ].filter(
          (key) =>
            JSON.stringify(current[key]) !== JSON.stringify(previous[key])
        )
        return { service, image: text(current.image), changed, path }
      }),
    [compose, before]
  )
  const deleted = configKeys(before, ['services']).filter(
    (name) => !summaries.some((item) => item.service === name)
  )
  return (
    <div className="max-h-44 space-y-1 overflow-y-auto overscroll-contain text-sm">
      {summaries.map((item) => (
        <div
          key={item.service}
          className="flex flex-wrap items-center gap-2 border-b py-2"
        >
          <span className="font-medium">{serviceLabel(item.service, zh)}</span>
          <span className="truncate text-xs text-muted-foreground">
            {item.image ||
              (zh ? '镜像由源码配置' : 'Image configured in source')}
          </span>
          {item.changed.map((field) => (
            <Button
              variant="link"
              size="sm"
              key={field}
              disabled={!onLocate}
              onClick={() => onLocate?.([...item.path, field])}
            >
              {field}
            </Button>
          ))}
        </div>
      ))}
      {deleted.map((name) => (
        <p key={name} className="text-destructive">
          {zh ? '删除服务：' : 'Removed service: '}
          {serviceLabel(name, zh)}
        </p>
      ))}
      {(['networks', 'volumes'] as const).flatMap((section) => {
        const names = [
          ...new Set([
            ...configKeys(compose, [section]),
            ...configKeys(before, [section])
          ])
        ]
        return names
          .filter(
            (name) =>
              JSON.stringify(configValue(compose, [section, name])) !==
              JSON.stringify(configValue(before, [section, name]))
          )
          .map((name) => (
            <Button
              key={`${section}/${name}`}
              variant="link"
              size="sm"
              disabled={!onLocate}
              onClick={() => onLocate?.([section, name])}
            >
              {section}.{name}
            </Button>
          ))
      })}
    </div>
  )
}
