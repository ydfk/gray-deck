import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { apiGet } from '@/api/client'
import { useI18n } from '@/i18n/I18nProvider'
import type { TrafficAnalysisReport, TrafficAnalysisRow, TrafficBytes, TrafficReport } from '@/types/api'
import { Panel } from './Panel'

type Subject = TrafficReport['items'][number]
type FormatBytes = (value: number) => string

function total(item: TrafficBytes) {
  return item.upload + item.download
}

function TrafficRanking({ title, items, formatBytes, select, unknown }: {
  title: string
  items: TrafficAnalysisRow[]
  formatBytes: FormatBytes
  select?: (value: string) => void
  unknown: string
}) {
  const biggest = Math.max(1, ...items.map(total))
  return (
    <section className="traffic-ranking">
      <h3>{title}</h3>
      <ul>
        {items.map((item) => (
          <li key={item.key}>
            {select && item.key ? (
              <button type="button" className="traffic-drilldown" onClick={() => select(item.key)}>{item.label || unknown}</button>
            ) : <span>{item.label || unknown}</span>}
            <strong>{formatBytes(total(item))}</strong>
            <progress max={biggest} value={total(item)} aria-hidden="true" />
          </li>
        ))}
      </ul>
    </section>
  )
}

function Breakdown({ subject, period, group, formatBytes }: {
  subject: Subject
  period: string
  group: 'user' | 'ip'
  formatBytes: FormatBytes
}) {
  const { t } = useI18n()
  const [day, setDay] = useState('')
  const [hour, setHour] = useState('')
  const [protocol, setProtocol] = useState('')
  const [target, setTarget] = useState('')
  const params = new URLSearchParams({ period, groupBy: group, kind: subject.kind, subject: subject.name, day, hour, protocol, target })
  const query = useQuery({
    queryKey: ['traffic-analysis', params.toString()],
    queryFn: () => apiGet<TrafficAnalysisReport>(`/api/traffic/analysis?${params}`),
    refetchInterval: 30_000,
  })
  if (query.isPending) return <p role="status">{t('common.loading')}</p>
  const report = query.data
  return (
    <>
      <div className="traffic-analysis-scope">
        <span>{t('traffic.analysisRange')} {hour ? hour.slice(0, 16).replace('T', ' ') : day || t('traffic.period')}</span>
        {protocol ? <span>{protocol.toUpperCase()}</span> : null}
        {target ? <span>{target}</span> : null}
        {day || protocol || target ? (
          <button type="button" className="secondary-pill" onClick={() => {
            setDay('')
            setHour('')
            setProtocol('')
            setTarget('')
          }}>{t('traffic.analysisReset')}</button>
        ) : null}
      </div>
      {query.isError ? (
        <p className="status-notice status-notice-error" role="alert">
          {t('traffic.analysisError')}{' '}
          <button type="button" className="secondary-pill" onClick={() => query.refetch()}>{t('traffic.retry')}</button>
        </p>
      ) : null}
      {report ? (
        <>
          <p className="body-copy">{t('traffic.analysisTracked', { tracked: formatBytes(total(report.totals)), summary: formatBytes(total(subject)) })}</p>
          {report.timeline.length === 0 ? <p className="body-copy">{t('traffic.analysisEmpty')}</p> : (
            <>
              <div className="traffic-analysis-grid">
                <TrafficRanking
                  title={t(hour ? 'traffic.analysisMinutes' : day ? 'traffic.analysisHours' : 'traffic.analysisDays')}
                  items={report.timeline} formatBytes={formatBytes} unknown="—"
                  select={hour ? undefined : (value) => day ? setHour(value) : setDay(value)}
                />
                <TrafficRanking title={t('traffic.analysisProtocols')} items={report.protocols.map((item) => ({ ...item, label: item.label.toUpperCase() }))}
                  formatBytes={formatBytes} select={setProtocol} unknown="—" />
                <TrafficRanking title={t('traffic.analysisTargets')} items={report.targets}
                  formatBytes={formatBytes} select={setTarget} unknown="—" />
              </div>
              <h3>{t('traffic.analysisAccesses')}</h3>
              <div className="table-shell traffic-connection-scroll" tabIndex={0} role="region" aria-label={t('traffic.analysisAccesses')}>
                <table className="table traffic-analysis-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('traffic.analysisRange')}</th>
                      <th scope="col">{t('traffic.connectionTarget')}</th>
                      <th scope="col">{t('traffic.connectionType')}</th>
                      <th scope="col">{t('traffic.connectionRoute')}</th>
                      <th scope="col" aria-sort="descending">{t('traffic.total')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {report.accesses.map(({ connection, first, last, ...usage }) => (
                      <tr key={connection.id}>
                        <td>
                          <time dateTime={first}>{first.slice(0, 16).replace('T', ' ')}</time>
                          {first !== last ? <div className="table-secondary"><time dateTime={last}>{last.slice(0, 16).replace('T', ' ')}</time></div> : null}
                        </td>
                        <td>
                          <div className="table-primary">{connection.host || connection.sniffHost || connection.destinationIP || '—'}</div>
                          {connection.destinationPort ? <div className="table-secondary">{t('traffic.analysisPort')} {connection.destinationPort}</div> : null}
                        </td>
                        <td>{[connection.network?.toUpperCase(), connection.inboundType].filter(Boolean).join(' / ') || '—'}</td>
                        <td>
                          <div className="table-primary">{connection.node || '—'}</div>
                          {connection.route && connection.route.length > 1 ? <div className="table-secondary">{connection.route.join(' → ')}</div> : null}
                        </td>
                        <td className="traffic-connection-usage">
                          <strong>{formatBytes(total(usage))}</strong>
                          <div className="table-secondary">↑ {formatBytes(usage.upload)} · ↓ {formatBytes(usage.download)}</div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </>
          )}
        </>
      ) : null}
    </>
  )
}

export function TrafficAnalysis({ period, group, subjects, selection, onSelect, formatBytes }: {
  period: string
  group: 'user' | 'ip'
  subjects: Subject[]
  selection: string
  onSelect: (value: string) => void
  formatBytes: FormatBytes
}) {
  const { t } = useI18n()
  const subject = subjects.find((item) => `${item.kind}:${item.name}` === selection)
  return (
    <div id="traffic-analysis" className="traffic-analysis">
      <Panel title={t('traffic.analysisTitle')} description={t('traffic.analysisDescription')}>
        <div className="traffic-filters">
          <label>
            <span>{t('traffic.subject')}</span>
            <select className="table-input" value={subject ? selection : ''} onChange={(event) => onSelect(event.target.value)}>
              <option value="">{t('traffic.analysisChoose')}</option>
              {subjects.map((item) => {
                const key = `${item.kind}:${item.name}`
                return <option key={key} value={key}>{item.name || t('traffic.unknown')}</option>
              })}
            </select>
          </label>
        </div>
        {subject ? <Breakdown key={`${period}:${group}:${selection}`} subject={subject} period={period} group={group} formatBytes={formatBytes} /> : null}
      </Panel>
    </div>
  )
}
