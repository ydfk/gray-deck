import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { apiGet } from '@/api/client'
import { useI18n } from '@/i18n/I18nProvider'
import type { TrafficReport } from '@/types/api'
import { Panel } from '@/ui/components/Panel'
import { TrafficConnections } from '@/ui/components/TrafficConnections'
import { TrafficAnalysis } from '@/ui/components/TrafficAnalysis'

const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB', 'EiB']

function displayBytes(bytes: number, numberFormat: Intl.NumberFormat) {
  let unit = 0
  while (bytes >= 1024 && unit < units.length - 1) {
    bytes /= 1024
    unit++
  }
  return `${numberFormat.format(bytes)} ${units[unit]}`
}

export function Component() {
  const { locale, t } = useI18n()
  const [period, setPeriod] = useState('')
  const [group, setGroup] = useState<'user' | 'ip'>('user')
  const [subject, setSubject] = useState('')
  const query = useQuery({
    queryKey: ['traffic', period, group],
    queryFn: () => apiGet<TrafficReport>(`/api/traffic?${new URLSearchParams({ period, groupBy: group })}`),
    placeholderData: keepPreviousData,
    refetchInterval: 10_000,
  })
  const report = query.data
  const numberFormat = new Intl.NumberFormat(locale, { maximumFractionDigits: 2 })
  const bytes = (size: number) => displayBytes(size, numberFormat)

  return (
    <div className="page-grid traffic-page">
      <Panel title={t('nav.traffic')} description={t('traffic.description')}>
        <div className="traffic-filters">
          <label>
            <span>{t('traffic.period')}</span>
            <select className="table-input" value={period} onChange={(event) => setPeriod(event.target.value)}>
              <option value="">
                {t(report && !report.periodOpen ? 'traffic.latest' : 'traffic.current')}{report ? ` · ${report.currentPeriod}` : ''}
              </option>
              {report?.periods.filter((item) => item.id !== report.currentPeriod).map((item) => (
                <option key={item.id} value={item.id}>{item.start} → {item.end}</option>
              ))}
              {period && !report?.periods.some((item) => item.id === period && item.id !== report.currentPeriod)
                ? <option value={period}>{period}</option>
                : null}
            </select>
          </label>
          <label>
            <span>{t('traffic.group')}</span>
            <select className="table-input" value={group} onChange={(event) => {
              setGroup(event.target.value === 'ip' ? 'ip' : 'user')
              setSubject('')
            }}>
              <option value="user">{t('traffic.byUser')}</option>
              <option value="ip">{t('traffic.byIP')}</option>
            </select>
          </label>
        </div>

        {report ? (
          <div>
            <p className="body-copy">{t('traffic.cycle', { start: report.cycleStart, end: report.cycleEnd, timezone: report.timezone })}</p>
            <p className="body-copy">{t('traffic.retention', { months: report.retentionMonths, size: report.maxSizeMiB })}</p>
          </div>
        ) : null}

        {query.isError ? (
          <div className="status-notice status-notice-error" role="alert">
            {t('traffic.loadFailed')}{' '}
            <button type="button" className="secondary-pill" onClick={() => query.refetch()}>{t('traffic.retry')}</button>
          </div>
        ) : null}
        {query.isPending || query.isPlaceholderData ? <p role="status">{t('common.loading')}</p> : null}

        {report && !query.isPlaceholderData ? (
          <>
            {!report.sampling ? <p className="status-notice">{t('traffic.paused')}</p> : null}
            {report.storageError ? <p className="status-notice status-notice-error" role="alert">{t('traffic.storageError')}</p> : null}
            {report.period.partial ? <p className="status-notice">{t('traffic.partial')}</p> : null}
            <div className="traffic-summary-heading">
              <strong>{report.period.start} → {report.period.end}</strong>
              {report.lastSampleAt ? <span>{t('traffic.sampledAt', { time: report.lastSampleAt })}</span> : null}
            </div>
            <dl className="traffic-totals">
              <div><dt>{t('traffic.upload')}</dt><dd>{bytes(report.totals.upload)}</dd></div>
              <div><dt>{t('traffic.download')}</dt><dd>{bytes(report.totals.download)}</dd></div>
              <div><dt>{t('traffic.total')}</dt><dd>{bytes(report.totals.upload + report.totals.download)}</dd></div>
            </dl>
            {report.items.length === 0 ? (
              <p className="body-copy">{t('traffic.empty')}</p>
            ) : (
              <div className="table-shell">
                <table className="table traffic-table">
                  <thead>
                    <tr>
                      <th scope="col">{t('traffic.subject')}</th>
                      <th scope="col">{t('traffic.upload')}</th>
                      <th scope="col">{t('traffic.download')}</th>
                      <th scope="col" aria-sort="descending">{t('traffic.total')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {report.items.map((item) => (
                      <tr key={`${item.kind}:${item.name}`}>
                        <td>
                          <a className="traffic-drilldown" href="#traffic-analysis" onClick={() => setSubject(`${item.kind}:${item.name}`)}>
                            {item.name || t('traffic.unknown')}
                          </a>
                          {item.kind !== 'unknown' ? <div className="table-secondary">{t(`traffic.${item.kind}`)}</div> : null}
                        </td>
                        <td>{bytes(item.upload)}</td>
                        <td>{bytes(item.download)}</td>
                        <td>{bytes(item.upload + item.download)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </>
        ) : null}
      </Panel>
      {report && !query.isPlaceholderData ? (
        <TrafficAnalysis
          period={report.period.id}
          group={group}
          subjects={report.items}
          selection={subject}
          onSelect={setSubject}
          formatBytes={bytes}
        />
      ) : null}
      {report && !query.isPlaceholderData ? (
        <TrafficConnections
          key={`${report.period.id}:${group}`}
          period={report.period.id}
          group={group}
          subjects={report.items}
          formatBytes={bytes}
        />
      ) : null}
    </div>
  )
}
