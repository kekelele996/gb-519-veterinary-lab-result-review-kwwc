
import { request } from './client';
import type { DomainRecord } from '../types/domain';

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
export async function openSignoffReview(id: number, reason: string, evidence: string) {
  return request<DomainRecord>(`/signoff/${id}/reviews`, {
    method: 'POST', body: JSON.stringify({ reason, evidence }),
  });
}
export async function decideSignoffReview(
  signoffId: number, reviewId: number, decision: 'upheld' | 'rejected', expectedVersion: number, reason: string,
) {
  return request<DomainRecord>(`/signoff/${signoffId}/reviews/decision`, {
    method: 'POST', body: JSON.stringify({ reviewId, decision, reason, expectedVersion }),
  });
}
