
import type { DomainRecord } from '../../types/domain';
import { EmptyState } from './EmptyState';
import { StatusBadge } from './StatusBadge';

export function ResultPanel({ records }: { records: DomainRecord[] }) {
  if (!records.length) return <EmptyState message="暂无可展示的结果证据" />;
  return <div className="evidence-strip">{records.slice(0, 4).map((item) => {
    const latest = item.revisions?.at(-1);
    const correctionStatus = item.openCorrection ? 'open' : item.latestCorrection?.status;
    const correctionLabel = correctionStatus === 'open' ? '复核中'
      : correctionStatus === 'approved' ? (item.superseded ? '已更正生效' : '已同意·待重签')
      : correctionStatus === 'rejected' ? '复核已驳回' : '';
    return <article key={item.id}>
      <div className="result-title"><strong>{item.code}</strong><StatusBadge status={item.status} /></div>
      <span>{item.name}</span>
      {item.superseded && <small className="superseded-note">已被更正替代 · 旧版可查{item.correctionCode ? `（${item.correctionCode}）` : ''}</small>}
      {item.correctionOfId ? <small className="correction-note">复核更正稿 · 原结果 {item.originalCode || `#${item.correctionOfId}`}</small> : null}
      {correctionLabel && <small className="correction-note">复核状态：{correctionLabel}{!item.superseded && item.correctionCode ? ` · 草稿 ${item.correctionCode}` : ''}</small>}
      <small title={item.evidence}>{item.evidence || '尚未附加证据'}</small>
      <small>v{item.version} · {latest?.actor || item.reviewedBy || item.preparedBy || item.owner}</small>
      <code title={latest?.requestId}>{latest?.requestId || '待形成签发请求 ID'}</code>
    </article>;
  })}</div>;
}
