import { useEffect, useMemo, useState } from 'react';
import type { EntityConfig, DomainRecord } from '../types/domain';
import type { EntityStore } from '../stores/factory';
import { nextStatus, formatDate } from '../utils/format';
import { StatusBadge } from './common/StatusBadge';
import { RiskTag } from './common/RiskTag';
import { ResultPanel } from './common/ResultPanel';
import { EmptyState } from './common/EmptyState';
import { MetricCard } from './common/MetricCard';
import { ConfirmDialog } from './common/ConfirmDialog';
import { UiButton } from './common/UiButton';
import { CorrectionDialog } from './CorrectionDialog';
import { CorrectionDecisionDialog } from './CorrectionDecisionDialog';
import { openSignoffCorrection, decideSignoffCorrection } from '../api/result-signoff';
import { useAuth } from '../hooks/useAuth';

interface EntityPageProps {
  config: EntityConfig;
  useStore: EntityStore;
  showRiskTags?: boolean;
  showResultPanel?: boolean;
  reviewSupport?: boolean;
}

export function EntityPage({ config, useStore, showRiskTags = false, showResultPanel = false, reviewSupport = false }: EntityPageProps) {
  const { items, meta, loading, error, load, createRecord, transition } = useStore();
  const { session, hasRole } = useAuth();
  const [search, setSearch] = useState('');
  const [showCreate, setShowCreate] = useState(false);
  const [pending, setPending] = useState<{ item: DomainRecord; status: string } | null>(null);
  const [openTarget, setOpenTarget] = useState<DomainRecord | null>(null);
  const [decideTarget, setDecideTarget] = useState<DomainRecord | null>(null);
  const [correctionError, setCorrectionError] = useState('');

  useEffect(() => { void load(config.path); }, [config.path, load]);
  const highRisk = useMemo(() => items.filter((item) => ['high', 'critical'].includes(item.riskLevel)).length, [items]);
  const canOperate = hasRole('operator');
  const canReview = hasRole('reviewer');

  const reload = () => load(config.path, search);

  const createDemo = async () => {
    const now = Date.now();
    await createRecord(config.path, {
      code: `${config.key.toUpperCase()}-${now.toString().slice(-6)}`,
      name: `新增${config.label}`,
      description: '通过前端工作台创建的业务记录',
      facility: '默认检验区', owner: session?.displayName || '现场操作员', category: '常规', riskLevel: 'medium',
      metricValue: 25, metricUnit: 'unit', effectiveAt: new Date().toISOString(), evidence: '已完成创建前证据核对', relatedCode: '',
    });
    setSearch('');
    setShowCreate(false);
  };

  const requiresPreparer = (item: DomainRecord) => config.key === 'resultSignoff' && item.status === 'draft';
  const requiresReviewer = (item: DomainRecord) => config.key === 'resultSignoff' && item.status === 'peer_review';
  const isOriginalPreparer = (item: DomainRecord) => Boolean(session?.username && session.username === item.preparedBy);
  const isOriginalSigner = (item: DomainRecord) => Boolean(session?.username && session.username === item.reviewedBy);
  const canAdvance = (item: DomainRecord) => canOperate
    && (!requiresPreparer(item) || isOriginalPreparer(item))
    && (!requiresReviewer(item) || (hasRole('reviewer') && !isOriginalPreparer(item)));

  const unavailableReason = (item: DomainRecord) => {
    if (!canOperate) return '只读';
    if (requiresPreparer(item) && !isOriginalPreparer(item)) return '等待制单人';
    if (requiresReviewer(item) && !hasRole('reviewer')) return '等待复核员';
    if (requiresReviewer(item) && isOriginalPreparer(item)) return '需异人复核';
    return '流程结束';
  };

  const confirmTransition = async () => {
    if (!pending) return;
    await transition(config.path, pending.item, pending.status);
    setSearch('');
    setPending(null);
  };

  // A reviewer may open a correction only on signed records they did not prepare/sign,
  // and only when no review is open and no approved correction draft is still in flight.
  const correctionInFlight = (item: DomainRecord) => Boolean(
    item.openCorrection
    || (item.latestCorrection?.status === 'approved' && !item.superseded && item.correctionCode),
  );
  const canOpenCorrection = (item: DomainRecord) => reviewSupport && canReview && item.status === 'signed'
    && !item.superseded && !correctionInFlight(item)
    && !isOriginalPreparer(item) && !isOriginalSigner(item);
  const correctionUnavailableReason = (item: DomainRecord) => {
    if (!canReview) return '仅复核员可操作';
    if (item.status !== 'signed') return '仅已签发可复核';
    if (item.superseded) return '已被更正替代';
    if (isOriginalPreparer(item) || isOriginalSigner(item)) return '需异人复核';
    return '已有在办复核';
  };

  const submitCorrection = async (reason: string, evidence: string) => {
    if (!openTarget) return;
    try {
      await openSignoffCorrection(openTarget.id, { reason, evidence });
      setOpenTarget(null);
      await reload();
    } catch (submitError) {
      setCorrectionError(submitError instanceof Error ? submitError.message : String(submitError));
      throw submitError;
    }
  };

  const submitDecision = async (approve: boolean, decisionNote: string) => {
    if (!decideTarget?.openCorrection) return;
    await decideSignoffCorrection(decideTarget.id, decideTarget.openCorrection.id, { approve, decisionNote });
    setDecideTarget(null);
    await reload();
  };

  const renderCorrectionCell = (item: DomainRecord) => {
    const independentReviewer = canReview && !isOriginalPreparer(item) && !isOriginalSigner(item);
    if (item.openCorrection) {
      const correction = item.openCorrection;
      return <div className="correction-cell">
        <span className="correction-chip correction-chip--open">复核中</span>
        <small>发起人 {correction.requestedBy}</small>
        {independentReviewer
          ? <button className="table-action" onClick={() => setDecideTarget(item)}>处理复核</button>
          : <small className="muted">需异人处理</small>}
      </div>;
    }
    if (item.superseded) {
      return <div className="correction-cell"><span className="correction-chip correction-chip--superseded">已被更正</span>{item.correctionCode && <small>替代版本 {item.correctionCode}</small>}<small className="muted">旧版可查</small></div>;
    }
    if (item.correctionOfId) {
      return <div className="correction-cell"><span className="correction-chip correction-chip--draft">更正稿</span><small>原结果 {item.originalCode || `#${item.correctionOfId}`}</small></div>;
    }
    if (item.latestCorrection?.status === 'approved' && item.correctionCode) {
      return <div className="correction-cell"><span className="correction-chip correction-chip--approved">已同意·待重签</span><small>新草稿 {item.correctionCode}</small><small>发起人 {item.latestCorrection.requestedBy}</small></div>;
    }
    if (item.latestCorrection?.status === 'rejected') {
      return <div className="correction-cell"><span className="correction-chip correction-chip--rejected">已驳回·原结果不变</span>
        {canOpenCorrection(item)
          ? <button className="table-action" onClick={() => { setCorrectionError(''); setOpenTarget(item); }}>再次发起复核</button>
          : <small className="muted">{correctionUnavailableReason(item)}</small>}
      </div>;
    }
    if (item.status === 'signed') {
      return canOpenCorrection(item)
        ? <button className="table-action" onClick={() => { setCorrectionError(''); setOpenTarget(item); }}>发起复核更正</button>
        : <span className="muted">{correctionUnavailableReason(item)}</span>;
    }
    return <span className="muted">-</span>;
  };

  return <main className="workspace">
    <header className="page-header"><div><p className="eyebrow">业务工作台</p><h1>{config.label}</h1><p>统一管理{config.label}的状态、风险、证据与责任人。</p></div>{canOperate ? <UiButton onClick={() => setShowCreate(true)}>新增{config.label}</UiButton> : <span className="access-note">只读权限</span>}</header>
    <section className="metrics"><MetricCard label="记录总数" value={meta.total} detail="当前筛选范围"/><MetricCard label="高风险" value={highRisk} detail="需要优先复核"/><MetricCard label="状态种类" value={new Set(items.map((item) => item.status)).size} detail="状态机覆盖"/></section>
    {showResultPanel && <section className="result-section"><header><h2>结果与版本证据</h2><span>签发版本、操作者和请求 ID 可追溯</span></header><ResultPanel records={items} /></section>}
    <section className="toolbar"><input aria-label="搜索" placeholder={`搜索${config.label}编码或名称`} value={search} onChange={(event) => setSearch(event.target.value)} /><UiButton onClick={() => void load(config.path, search)}>查询</UiButton><button className="link-button" onClick={() => { setSearch(''); void load(config.path); }}>重置</button></section>
    {(error || correctionError) && <div className="alert" role="alert">{error || correctionError}</div>}
    <section className="table-shell" aria-busy={loading}><table><thead><tr><th>编码</th><th>名称</th><th>状态</th>{reviewSupport && <th>复核更正</th>}<th>风险</th><th>责任人</th><th>指标</th><th>更新时间</th><th>操作</th></tr></thead><tbody>
      {items.map((item) => { const next = nextStatus(item.status, config.primaryTransitions); return <tr key={item.id}><td><strong>{item.code}</strong>{reviewSupport && item.superseded && <small className="superseded-note">旧版可查</small>}</td><td>{item.name}<small>{item.facility}</small></td><td><StatusBadge status={item.status}/></td>{reviewSupport && <td>{renderCorrectionCell(item)}</td>}<td>{showRiskTags ? <RiskTag level={item.riskLevel}/> : item.riskLevel}</td><td>{item.owner}</td><td>{item.metricValue} {item.metricUnit}</td><td>{formatDate(item.updatedAt)}</td><td>{next && canAdvance(item) ? <button className="table-action" onClick={() => setPending({ item, status: next })}>推进至 {next}</button> : <span className="muted">{next ? unavailableReason(item) : '流程结束'}</span>}</td></tr>; })}
      {!items.length && !loading && <EmptyState message="暂无记录" colSpan={reviewSupport ? 9 : 8} />}
    </tbody></table>{loading && <div className="loading">正在同步业务数据…</div>}</section>
    <ConfirmDialog open={showCreate} title={`新增${config.label}`} onCancel={() => setShowCreate(false)} onConfirm={() => void createDemo().catch(() => undefined)}><p>将创建一条包含完整责任人、风险和证据信息的演示记录。</p></ConfirmDialog>
    <ConfirmDialog open={Boolean(pending)} title="确认状态迁移" onCancel={() => setPending(null)} onConfirm={() => void confirmTransition().catch(() => undefined)}><p>状态迁移会写入不可覆盖的版本与审计日志。</p><strong>{pending?.item.status} → {pending?.status}</strong></ConfirmDialog>
    {reviewSupport && <CorrectionDialog open={Boolean(openTarget)} record={openTarget} onCancel={() => setOpenTarget(null)} onSubmit={submitCorrection} />}
    {reviewSupport && <CorrectionDecisionDialog open={Boolean(decideTarget)} record={decideTarget} onCancel={() => setDecideTarget(null)} onDecide={submitDecision} />}
  </main>;
}
