import { useEffect, useState } from 'react';
import { UiButton } from './common/UiButton';
import type { DomainRecord } from '../types/domain';

interface CorrectionDecisionDialogProps {
  open: boolean;
  record: DomainRecord | null;
  onCancel: () => void;
  onDecide: (approve: boolean, decisionNote: string) => Promise<void>;
}

export function CorrectionDecisionDialog({ open, record, onCancel, onDecide }: CorrectionDecisionDialogProps) {
  const [note, setNote] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (open) {
      setNote('');
      setError('');
      setSubmitting(false);
    }
  }, [open]);

  if (!open || !record?.openCorrection) return null;
  const correction = record.openCorrection;

  const decide = async (approve: boolean) => {
    setSubmitting(true);
    setError('');
    try {
      await onDecide(approve, note.trim());
    } catch (submitError) {
      setError(submitError instanceof Error ? submitError.message : String(submitError));
      setSubmitting(false);
    }
  };

  return <div className="modal-backdrop">
    <section className="modal" role="dialog" aria-modal="true">
      <h2>复核更正决定</h2>
      <div className="correction-evidence">
        <p><strong>{record.code}</strong> · 发起人 {correction.requestedBy}</p>
        <small>原因：{correction.reason}</small>
        <small title={correction.evidence}>证据：{correction.evidence}</small>
      </div>
      <label className="field-label">决定备注（可选）
        <textarea aria-label="决定备注" value={note} maxLength={500} rows={3}
          placeholder="记录复核决定依据" onChange={(event) => setNote(event.target.value)} />
      </label>
      {error && <div className="alert" role="alert">{error}</div>}
      <footer>
        <button className="link-button" onClick={onCancel} disabled={submitting}>取消</button>
        <UiButton onClick={() => void decide(false)}>驳回（原结果不变）</UiButton>
        <UiButton onClick={() => void decide(true)}>同意并建更正草稿</UiButton>
      </footer>
    </section>
  </div>;
}
