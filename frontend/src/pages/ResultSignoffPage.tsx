
import { EntityPage } from '../components/EntityPage';
import { SignoffReviewFeature } from '../components/SignoffReviewFeature';
import { ENTITY_CONFIGS } from '../types/status';
import { useResultSignoffStore } from '../stores/result-signoff';

export default function ResultSignoffPage() {
  const review = SignoffReviewFeature();
  return <EntityPage
    config={ENTITY_CONFIGS[3]}
    useStore={useResultSignoffStore}
    showResultPanel
    extraColumns={[{ header: '复核更正', render: review.renderCell }]}
    extraDialogs={review.dialogs}
  />;
}
