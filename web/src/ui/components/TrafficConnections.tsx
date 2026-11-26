import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { apiGet } from '@/api/client'
import { useI18n } from '@/i18n/I18nProvider'
import type { TrafficConnection, TrafficConnectionReport, TrafficReport } from '@/types/api'
import { Panel } from './Panel'

type Props = {
  period: string;
  group: 'user' | 'ip';
  subjects: TrafficReport['items'];
  formatBytes: (value: number) => string;
};

function endpoint(host?: string, port?: string) {
  if (!host || !port) return host
  return `${host.includes(':') ? `[${host}]` : host}:${port}`
}

function localTime(value: string) {
  return value.slice(0, 19).replace('T', ' ')
}

function ConnectionRow({ item, formatBytes }: { item: TrafficConnection; formatBytes: Props['formatBytes'] }) {
  const { t } = useI18n()
  const host = item.host || item.sniffHost || item.destinationIP
  const address = item.destinationIP !== host ? endpoint(item.destinationIP, item.destinationPort) : null
  const request = [item.network?.toUpperCase(), item.inboundType].filter(Boolean).join(' / ')
  const rule = [item.rule, item.rulePayload].filter(Boolean).join(' · ')
  return (
    <tr>
      <td>
        <time className="table-primary" dateTime={item.startedAt}>{localTime(item.startedAt)}</time>
        <div className="table-secondary">
          <time dateTime={item.lastSeenAt}>{t('traffic.sampledAt', { time: localTime(item.lastSeenAt) })}</time>
        </div>
      </td>
      <td>
        <div className="table-primary">{endpoint(host, item.destinationPort) || '—'}</div>
        {address ? <div className="table-secondary">{address}</div> : null}
        {item.sniffHost && item.sniffHost !== host ? (
          <div className="table-secondary">{t('traffic.sniffedDomain', { host: item.sniffHost })}</div>
        ) : null}
      </td>
      <td>
        <div className="table-primary">{request || '—'}</div>
        {item.inboundName ? <div className="table-secondary">{item.inboundName}</div> : null}
      </td>
      <td>
        <div className="table-primary">{item.inboundUser || item.sourceIP || t('traffic.unknown')}</div>
        {item.inboundUser && item.sourceIP ? <div className="table-secondary">{item.sourceIP}</div> : null}
      </td>
      <td>
        <div className="table-primary">{item.node || '—'}</div>
        {item.route && item.route.length > 1 ? <div className="table-secondary">{item.route.join(' → ')}</div> : null}
        {rule ? <div className="table-secondary">{t('traffic.rule', { rule })}</div> : null}
      </td>
      <td className="traffic-connection-usage">
        <div>{t('traffic.upload')} {formatBytes(item.upload)}</div>
        <div>{t('traffic.download')} {formatBytes(item.download)}</div>
      </td>
    </tr>
  )
}

export function TrafficConnections({ period, group, subjects, formatBytes }: Props) {
  const { t } = useI18n()
  const [selection, setSelection] = useState('')
  const [allRecent, setAllRecent] = useState(false)
  const [draft, setDraft] = useState('')
  const [search, setSearch] = useState('')
  const [cursors, setCursors] = useState([''])
  const cursor = cursors[cursors.length - 1]
  const chosen = subjects.find((item) => `${item.kind}:${item.name}` === selection)
  const params = new URLSearchParams({
    period: allRecent ? 'all' : period, groupBy: group, kind: chosen?.kind ?? '', subject: chosen?.name ?? '', search, cursor,
  })
  const query = useQuery({
    queryKey: ['traffic-connections', params.toString()],
    queryFn: () => apiGet<TrafficConnectionReport>(`/api/traffic/connections?${params}`),
    refetchInterval: cursor ? false : 10_000,
    refetchOnWindowFocus: !cursor,
  })
  const report = query.data
  const disabled = report?.retentionDays === 0

  return (
    <Panel title={t('traffic.connections')} description={t('traffic.connectionDescription')}>
      {disabled ? <p className="body-copy">{t('traffic.connectionsDisabled')}</p> : (
        <form className="traffic-filters traffic-connection-filters" onSubmit={(event) => {
          event.preventDefault()
          setSearch(draft.trim())
          setCursors([''])
        }}>
          <label>
            <span>{t('traffic.connectionScope')}</span>
            <select className="table-input" value={allRecent ? 'all' : 'period'} onChange={(event) => {
              setAllRecent(event.target.value === 'all')
              setCursors([''])
            }}>
              <option value="period">{t('traffic.period')}</option>
              <option value="all">{t('traffic.allRecent')}</option>
            </select>
          </label>
          <label>
            <span>{t('traffic.subject')}</span>
            <select className="table-input" value={chosen ? selection : ''} onChange={(event) => {
              setSelection(event.target.value)
              setCursors([''])
            }}>
              <option value="">{t('traffic.allSubjects')}</option>
              {subjects.map((item) => {
                const key = `${item.kind}:${item.name}`
                return <option key={key} value={key}>{item.name || t('traffic.unknown')}</option>
              })}
            </select>
          </label>
          <label className="traffic-connection-search">
            <span>{t('traffic.connectionSearch')}</span>
            <input className="table-input" type="search" value={draft} maxLength={160} onChange={(event) => setDraft(event.target.value)} />
          </label>
          <button type="submit" className="secondary-pill">{t('traffic.search')}</button>
        </form>
      )}
      {query.isError ? (
        <p className="status-notice status-notice-error" role="alert">
          {t('traffic.connectionLoadFailed')}{' '}
          <button type="button" className="secondary-pill" onClick={() => query.refetch()}>{t('traffic.retry')}</button>
        </p>
      ) : null}
      {query.isPending ? <p role="status">{t('common.loading')}</p> : null}
      {report && !disabled ? (
        <>
          <p className="body-copy">{t('traffic.connectionRetention', { days: report.retentionDays, size: report.maxSizeMiB, count: report.maxRecords })}</p>
          {report.storageError ? <p className="status-notice status-notice-error" role="alert">{t('traffic.connectionSaveFailed')}</p> : null}
          {report.items.length === 0 ? (
            <p className="body-copy">{t('traffic.connectionEmpty')}</p>
          ) : (
            <div className="table-shell traffic-connection-scroll" tabIndex={0} role="region" aria-label={t('traffic.connections')}>
              <table className="table traffic-connection-table">
                <thead>
                  <tr>
                    <th scope="col" aria-sort="descending">{t('traffic.connectionTime')}</th>
                    <th scope="col">{t('traffic.connectionTarget')}</th>
                    <th scope="col">{t('traffic.connectionType')}</th>
                    <th scope="col">{t('traffic.subject')}</th>
                    <th scope="col">{t('traffic.connectionRoute')}</th>
                    <th scope="col">{t('traffic.connectionBytes')}</th>
                  </tr>
                </thead>
                <tbody>
                  {report.items.map((item) => <ConnectionRow key={item.id} item={item} formatBytes={formatBytes} />)}
                </tbody>
              </table>
            </div>
          )}
        </>
      ) : null}
      {cursors.length > 1 || report?.nextCursor ? (
        <nav className="traffic-connection-pagination" aria-label={t('traffic.connectionPagination')}>
          <button type="button" className="secondary-pill" disabled={cursors.length === 1} onClick={() => setCursors(cursors.slice(0, -1))}>
            {t('traffic.previousPage')}
          </button>
          <span>{t('traffic.connectionPage', { page: cursors.length })}</span>
          <button type="button" className="secondary-pill" disabled={!report?.nextCursor} onClick={() => {
            if (report?.nextCursor) setCursors([...cursors, report.nextCursor])
          }}>
            {t('traffic.nextPage')}
          </button>
        </nav>
      ) : null}
    </Panel>
  )
}
