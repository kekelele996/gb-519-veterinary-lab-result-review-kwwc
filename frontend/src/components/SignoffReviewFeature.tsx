import { useState, type ReactNode } from 'react';
import type { DomainRecord, SignoffReview } from '../types/domain';
import { SIGNOFF_REVIEW_LABELS } from '../types/status';
import { formatDate } from '../utils/format';
import { StatusBadge } from './common/StatusBadge';
import { UiButton } from './common/UiButton';
import { useAuth } from '../hooks/useAuth';
import { useResultSignoffStore } from '../stores/result-signoff';

function openReviewOf(item: DomainRecord): SignoffReview | undefined {
  return item.reviews?.find((review) => review.status === 'open');
}

function reviewTone(status: SignoffReview['status']): string {
  if (status === 'open') return 'status status--warning';
  if (status === 'upheld') return 'status status--success';
  return 'status status--danger';
}

// SignoffReviewFeature renders the "复核更正" column cell for a signoff row and
// owns the open/decision dialogs. Refresh consistency is provided by the store
// reload that follows every successful mutation.
export function SignoffReviewFeature() {
  const { session, hasRole } = useAuth();
  const openReviewAction = useResultSignoffStore((state) => state.openReview);
  const resolveReview = useResultSignoffStore((state) => state.resolveReview);

  const [opening, setOpening] = useState<DomainRecord | null>(null);
  const [openReason, setOpenReason] = useState('');
  const [openEvidence, setOpenEvidence] = useState('');
  const [deciding, setDeciding] = useState<{ item: DomainRecord; review: SignoffReview; decision: 'upheld' | 'rejected' } | null>(null);
  const [decisionReason, setDecisionReason] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const isReviewer = hasRole('reviewer');
  const isNotSigner = (item: DomainRecord) => Boolean(session?.username) && session?.username !== item.reviewedBy;

  const resetOpen = () => { setOpening(null); setOpenReason(''); setOpenEvidence(''); };
  const resetDecide = () => { setDeciding(null); setDecisionReason(''); };

  const submitOpen = async () => {
    if (!opening) return;
    setSubmitting(true);
    try {
      await openReviewAction(opening.id, openReason.trim(), openEvidence.trim());
      resetOpen();
    } catch {
      // the store surfaces the error banner above the table; keep the dialog open
    } finally { setSubmitting(false); }
  };

  const submitDecision = async () => {
    if (!deciding) return;
    setSubmitting(true);
    try {
      await resolveReview(deciding.item.id, deciding.review.id, deciding.decision, decisionReason.trim());
      resetDecide();
    } catch {
      // keep dialog open so the reviewer can retry after refresh
    } finally { setSubmitting(false); }
  };

  const renderCell = (item: DomainRecord): ReactNode => {
    // Correction drafts point back at the signed version they replace.
    if (item.correctionOfId && item.correctionSource) {
      return <div className="review-cell">
        <small>更正自 {item.correctionSource.code}</small>
        <span className="muted">原签发：<StatusBadge status={item.correctionSource.status} /></span>
      </div>;
    }
    if (item.status !== 'signed') return <span className="muted">—</span>;

    const open = openReviewOf(item);
    if (open) {
      return <div className="review-cell">
        <span className={reviewTone(open.status)}>{SIGNOFF_REVIEW_LABELS.open}</span>
        <small title={open.reason}>{open.openedBy} 发起：{open.reason}</small>
        <small title={open.evidence} className="review-evidence">证据：{open.evidence}</small>
        {isReviewer && isNotSigner(item)
          ? <div className="review-actions">
              <button className="table-action" onClick={() => { setDeciding({ item, review: open, decision: 'upheld' }); setDecisionReason(''); }}>同意·建更正草稿</button>
              <button className="table-action" onClick={() => { setDeciding({ item, review: open, decision: 'rejected' }); setDecisionReason(''); }}>驳回</button>
            </div>
          : <small className="muted">等待异于签发人的复核员裁决</small>}
      </div>;
    }

    const closed = item.reviews?.filter((review) => review.status !== 'open') ?? [];
    const latest = closed.at(-1);
    const drafts = item.correctionDrafts ?? [];
    return <div className="review-cell">
      {latest && <>
        <span className={reviewTone(latest.status)}>{SIGNOFF_REVIEW_LABELS[latest.status]}</span>
        <small title={latest.decisionReason}>{latest.decidedBy} · {formatDate(latest.decidedAt || latest.createdAt)}</small>
      </>}
      {drafts.length > 0 && drafts.map((draft) => (
        <small key={draft.id} className="review-draft">更正草稿 <strong>{draft.code}</strong> · <StatusBadge status={draft.status} /></small>
      ))}
      {isReviewer && isNotSigner(item)
        ? <UiButton onClick={() => { setOpening(item); setOpenReason(''); setOpenEvidence(''); }}>发起复核</UiButton>
        : <small className="muted">{!isReviewer ? '等待复核员' : '原签发人不可复核'}</small>}
    </div>;
  };

  const dialogs = <>
    {opening && <div className="modal-backdrop"><section className="modal modal--form" role="dialog" aria-modal="true">
      <h2>对 {opening.code} 发起复核更正</h2>
      <p className="muted">仅复核员/管理员可发起，且复核人必须异于原签发人（{opening.reviewedBy || '-'}）。须填写复核原因与证据。</p>
      <label className="form-row">复核原因<textarea rows={3} value={openReason} maxLength={500} placeholder="说明已签发结果为何需要复核" onChange={(event) => setOpenReason(event.target.value)} /></label>
      <label className="form-row">证据<textarea rows={4} value={openEvidence} maxLength={2000} placeholder="附上原始记录、复测数据等证据" onChange={(event) => setOpenEvidence(event.target.value)} /></label>
      <footer><button className="link-button" onClick={resetOpen} disabled={submitting}>取消</button><UiButton onClick={() => void submitOpen()} disabled={submitting || openReason.trim().length < 3 || openEvidence.trim().length < 3}>提交复核</UiButton></footer>
    </section></div>}
    {deciding && <div className="modal-backdrop"><section className="modal modal--form" role="dialog" aria-modal="true">
      <h2>{deciding.decision === 'upheld' ? '同意复核并创建更正草稿' : '驳回复核'}</h2>
      <p className="muted">{deciding.decision === 'upheld'
        ? '原 signed 版本原样保留可查；系统将复制结果创建关联 draft，由制单人重新提交并经异人签发后生效。'
        : '仅关闭复核工单，原签发结果不变。'}</p>
      <label className="form-row">裁决原因<textarea rows={3} value={decisionReason} maxLength={500} placeholder="裁决依据，将写入版本证据与审计日志" onChange={(event) => setDecisionReason(event.target.value)} /></label>
      <footer><button className="link-button" onClick={resetDecide} disabled={submitting}>取消</button><UiButton onClick={() => void submitDecision()} disabled={submitting || decisionReason.trim().length < 3}>确认{deciding.decision === 'upheld' ? '并建草稿' : '驳回'}</UiButton></footer>
    </section></div>}
  </>;

  return { renderCell, dialogs };
}
