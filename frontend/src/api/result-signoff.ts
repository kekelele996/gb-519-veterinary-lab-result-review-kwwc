
import { request } from './client';
import type { DomainRecord, SignoffCorrection } from '../types/domain';

export async function listResultSignoff(page = 1, pageSize = 20, search = '') {
  return request<DomainRecord[]>(`/signoff?page=${page}&pageSize=${pageSize}&search=${encodeURIComponent(search)}`);
}
export async function createResultSignoff(input: Partial<DomainRecord>) {
  return request<DomainRecord>('/signoff', { method: 'POST', body: JSON.stringify(input) });
}
export async function transitionResultSignoff(id: number, status: string, expectedVersion: number, reason: string) {
  return request<DomainRecord>(`/signoff/${id}/transition`, {
    method: 'POST', body: JSON.stringify({ status, expectedVersion, reason }),
  });
}

export interface OpenCorrectionInput { reason: string; evidence: string }
export interface CorrectionDecisionInput { approve: boolean; decisionNote?: string }

export async function openSignoffCorrection(signoffId: number, input: OpenCorrectionInput) {
  return request<SignoffCorrection>(`/signoff/${signoffId}/corrections`, {
    method: 'POST', body: JSON.stringify(input),
  });
}

export async function decideSignoffCorrection(signoffId: number, correctionId: number, input: CorrectionDecisionInput) {
  return request<SignoffCorrection>(`/signoff/${signoffId}/corrections/${correctionId}/decision`, {
    method: 'POST', body: JSON.stringify(input),
  });
}
