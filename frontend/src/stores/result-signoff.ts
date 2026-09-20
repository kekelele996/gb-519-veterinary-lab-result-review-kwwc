
import { create } from 'zustand';
import { request } from '../api/client';
import { decideSignoffReview, openSignoffReview } from '../api/result-signoff';
import type { ApiEnvelope, DomainRecord, PageMeta } from '../types/domain';

interface SignoffState {
  items: DomainRecord[];
  meta: PageMeta;
  loading: boolean;
  error: string;
  load: (path?: string, search?: string) => Promise<void>;
  createRecord: (path: string, input: Partial<DomainRecord>) => Promise<void>;
  transition: (path: string, item: DomainRecord, status: string) => Promise<void>;
  openReview: (id: number, reason: string, evidence: string) => Promise<void>;
  resolveReview: (signoffId: number, reviewId: number, decision: 'upheld' | 'rejected', reason: string) => Promise<void>;
}

export const useResultSignoffStore = create<SignoffState>((set, get) => ({
  items: [], meta: { page: 1, pageSize: 20, total: 0 }, loading: false, error: '',
  load: async (_path = 'signoff', search = '') => {
    set({ loading: true, error: '' });
    try {
      const result = await request<DomainRecord[]>(`/signoff?page=1&pageSize=20&search=${encodeURIComponent(search)}`);
      set({ items: result.data, meta: result.meta || { page: 1, pageSize: 20, total: result.data.length }, loading: false });
    } catch (error) { set({ error: error instanceof Error ? error.message : String(error), loading: false }); }
  },
  createRecord: async (path, input) => {
    set({ loading: true, error: '' });
    try {
      await request<DomainRecord>(`/${path}`, { method: 'POST', body: JSON.stringify(input) });
      await get().load(path);
    } catch (error) { set({ error: error instanceof Error ? error.message : String(error), loading: false }); throw error; }
  },
  transition: async (path, item, status) => {
    set({ loading: true, error: '' });
    try {
      await request<DomainRecord>(`/${path}/${item.id}/transition`, { method: 'POST', body: JSON.stringify({ status, expectedVersion: item.version, reason: '前端工作台人工确认' }) });
      await get().load(path);
    } catch (error) { set({ error: error instanceof Error ? error.message : String(error), loading: false }); throw error; }
  },
  openReview: async (id, reason, evidence) => {
    set({ loading: true, error: '' });
    try {
      await openSignoffReview(id, reason, evidence);
      await get().load('signoff');
    } catch (error) { set({ error: error instanceof Error ? error.message : String(error), loading: false }); throw error; }
  },
  resolveReview: async (signoffId, reviewId, decision, reason) => {
    set({ loading: true, error: '' });
    try {
      // expectedVersion is the review id; the service verifies the review is still open.
      await decideSignoffReview(signoffId, reviewId, decision, reviewId, reason);
      await get().load('signoff');
    } catch (error) { set({ error: error instanceof Error ? error.message : String(error), loading: false }); throw error; }
  },
}));
