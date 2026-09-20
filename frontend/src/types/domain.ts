
export interface DomainRecord {
  id: number;
  code: string;
  name: string;
  status: string;
  version: number;
  description: string;
  facility: string;
  owner: string;
  category: string;
  riskLevel: 'low' | 'medium' | 'high' | 'critical';
  metricValue: number;
  metricUnit: string;
  effectiveAt: string;
  evidence: string;
  relatedCode: string;
  preparedBy?: string;
  reviewedBy?: string;
  reviewReason?: string;
  correctionOfId?: number;
  revisions?: SignoffRevision[];
  reviews?: SignoffReview[];
  correctionDrafts?: DomainRecord[];
  correctionSource?: DomainRecord;
  createdAt: string;
  updatedAt: string;
}

export interface SignoffReview {
  id: number;
  resultSignoffId: number;
  status: 'open' | 'upheld' | 'rejected';
  reason: string;
  evidence: string;
  openedBy: string;
  decidedBy?: string;
  decisionReason?: string;
  draftId?: number;
  createdAt: string;
  decidedAt?: string;
}

export interface SignoffRevision {
  id: number;
  version: number;
  status: string;
  evidence: string;
  actor: string;
  requestId: string;
  action: string;
  reason: string;
  createdAt: string;
}

export interface PageMeta { page: number; pageSize: number; total: number }
export interface ApiEnvelope<T> { data: T; error?: string; message?: string; meta?: PageMeta }
export interface UserSession { token: string; username: string; displayName: string; role: string; expiresIn: number }
export interface AuditLog {
  id: number; requestId: string; actor: string; action: string; entityType: string;
  entityId: number; beforeState: string; afterState: string; detail: string; createdAt: string;
}
export interface EntityConfig {
  key: string;
  path: string;
  label: string;
  statuses: readonly string[];
  primaryTransitions: Readonly<Record<string, string>>;
}
