import { useEffect, useState } from 'react';
import { UiButton } from './common/UiButton';
import type { DomainRecord } from '../types/domain';

interface CorrectionDialogProps {
  open: boolean;
  record: DomainRecord | null;
  onCancel: () => void;
  onSubmit: (reason: string, evidence: string) => Promise<void>;
}

export function CorrectionDialog({ open, record, onCancel, onSubmit }: CorrectionDialogProps) {
  const [reason, setReason] = useState('');
  const [evidence, setEvidence] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (open) {
      setReason('');
      setEvidence('');
      setError('');
      setSubmitting(false);
    }
  }, [open]);

  if (!open || !record) return null;

  const submit = async () => {
    if (reason.trim().length < 3 || evidence.trim().length < 3) {
      setError('复核原因与原因证据均为必填，且至少 3 个字符。');
      return;
    }
    setSubmitting(true);
    setError('');
    try {
      await onSubmit(reason.trim(), evidence.trim());
    } catch (submitError) {
      setError(submitError instanceof Error ? submitError.message : String(submitError));
      setSubmitting(false);
    }
  };

  return <div className="modal-backdrop">
    <section className="modal" role="dialog" aria-modal="true">
      <h2>发起签发复核更正</h2>
      <p className="correction-target">对已签发结果 <strong>{record.code}</strong> 发起复核。同意后保留原签发版本并生成关联草稿；驳回仅关闭复核，原结果不变。</p>
      <label className="field-label">复核原因
        <textarea aria-label="复核原因" value={reason} maxLength={500} rows={3}
          placeholder="说明为何需要复核更正该已签发结果" onChange={(event) => setReason(event.target.value)} />
      </label>
      <label className="field-label">原因证据
        <textarea aria-label="原因证据" value={evidence} maxLength={2000} rows={4}
          placeholder="附上复检图谱、样本追溯、质控记录等证据" onChange={(event) => setEvidence(event.target.value)} />
      </label>
      {error && <div className="alert" role="alert">{error}</div>}
      <footer>
        <button className="link-button" onClick={onCancel} disabled={submitting}>取消</button>
        <UiButton onClick={() => void submit()}>{submitting ? '提交中…' : '发起复核'}</UiButton>
      </footer>
    </section>
  </div>;
}
