import { useState } from 'react'
import { Eye, EyeOff, Plus, Trash2 } from 'lucide-react'
import { Input } from '../../components/ui/input'
import { Button } from '../../components/ui/button'
import { Checkbox } from '../../components/ui/checkbox'
import { Spinner } from '../../components/ui/spinner'
import { EnvironmentQuickAdd } from './environment-quick-add'
import type { CDEnvironmentVariable } from './types'
export function EnvironmentFields({variables,onChange,zh,environmentValues,showEnvironment,environmentLoading,environmentError,toggleEnvironment}:{
variables:CDEnvironmentVariable[];onChange:(value:CDEnvironmentVariable[])=>void;zh:boolean;environmentValues:Record<string,string>|null;showEnvironment:boolean;environmentLoading:boolean;environmentError:string;toggleEnvironment:()=>void|Promise<void>
}){
const [quickEnvironment,setQuickEnvironment]=useState(false)
return <>
      <div className="flex min-w-0 flex-col gap-2">
        {(variables).map((variable, index) => {
          const update = (patch: Partial<typeof variable>) => onChange(variables.map((row,rowIndex)=>rowIndex===index?{...row,...patch}:row))
          const savedValue = variable.has_value ? environmentValues?.[variable.name] : undefined
          return <div key={index} className="flex min-w-0 flex-wrap items-center gap-2 rounded-lg bg-muted/35 p-2 sm:flex-nowrap" data-testid="cd-environment-row">
            <Input aria-label={`${zh ? '变量名称' : 'Variable name'} ${index + 1}`} className="min-w-0 flex-1 font-mono sm:max-w-64" value={variable.name} maxLength={256} readOnly={variable.has_value} onChange={event => update({ name: event.target.value })} placeholder="APP_ENV" autoComplete="off" />
            <div className="order-3 flex w-full min-w-0 gap-2 sm:order-none sm:w-auto sm:flex-1">
              <Input aria-label={`${zh ? '变量值' : 'Variable value'} ${index + 1}`} type={showEnvironment ? 'text' : 'password'} className="font-mono" value={variable.value ?? savedValue ?? ''} onChange={event => update({ value: event.target.value })} placeholder={variable.has_value && variable.value === undefined && savedValue === undefined ? (zh ? '已保存 · 显示后可查看' : 'Saved · show to view') : (zh ? '空值' : 'Empty value')} autoComplete="new-password" spellCheck={false} />
              <label className="flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground"><Checkbox aria-label={`${zh ? '使用空值' : 'Use empty value'} ${index + 1}`} checked={variable.value === ''} onCheckedChange={checked => update({ value: checked ? '' : undefined })} /><span aria-hidden="true">{zh ? '空值' : 'Empty'}</span></label>
            </div>
            <Button type="button" variant="ghost" size="icon-sm" className="text-muted-foreground" aria-label={`${zh ? '删除变量' : 'Remove variable'} ${index + 1}`} onClick={() => onChange(variables.filter((_,rowIndex)=>rowIndex!==index))}><Trash2 /></Button>
          </div>
        })}
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Button type="button" variant="outline" size="sm" disabled={(variables.length) >= 100} onClick={() => onChange([...variables,{name:'',value:undefined}])}><Plus />{zh ? '添加环境变量' : 'Add variable'}</Button>
        <Button type="button" variant="ghost" size="sm" onClick={() => setQuickEnvironment(true)}>{zh ? '快速添加' : 'Quick add'}</Button>
        {!!variables.length && <Button type="button" variant="ghost" size="sm" disabled={environmentLoading} aria-busy={environmentLoading} aria-pressed={showEnvironment} onClick={() => void toggleEnvironment()}>{environmentLoading ? <Spinner aria-hidden="true" /> : showEnvironment ? <EyeOff /> : <Eye />}{environmentLoading ? (zh ? '读取中…' : 'Loading…') : showEnvironment ? (zh ? '隐藏变量值' : 'Hide variable values') : (zh ? '显示变量值' : 'Show variable values')}</Button>}
      </div>
      {environmentError && <p role="alert" className="text-sm text-destructive">{environmentError}</p>}
      {quickEnvironment && <EnvironmentQuickAdd variables={variables} zh={zh} onAdd={onChange} onClose={() => setQuickEnvironment(false)} />}

</>
}
