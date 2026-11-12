import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ApiError } from '../api/client';
import { getLlmStatus } from '../api/llm';
import { t } from '../i18n/en';
import { Button } from '../ui/Button';
import { getAdminLlmModels, getAdminLlmUsage, setAdminLlmModel } from './api';

const modelsKey = ['admin-llm-models'] as const;
const usageKey = ['admin-llm-usage'] as const;

function errorMessage(error: unknown): string {
  return error instanceof ApiError && error.serverMessage ? error.serverMessage : t('requestFailed');
}

export function AdminPage() {
  const queryClient = useQueryClient();
  const llmStatus = useQuery({ queryKey: ['llm-status'], queryFn: getLlmStatus, retry: false });
  const modelsQuery = useQuery({
    queryKey: modelsKey,
    queryFn: getAdminLlmModels,
    enabled: llmStatus.data?.enabled === true,
    retry: false,
  });
  const usageQuery = useQuery({ queryKey: usageKey, queryFn: getAdminLlmUsage, retry: false });
  const [selectedModel, setSelectedModel] = useState('');

  useEffect(() => {
    if (modelsQuery.data) setSelectedModel(modelsQuery.data.selected_model);
  }, [modelsQuery.data]);

  const modelMutation = useMutation({
    mutationFn: setAdminLlmModel,
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: modelsKey });
    },
  });

  async function refresh() {
    await Promise.all([usageQuery.refetch(), llmStatus.refetch()]);
    if (llmStatus.data?.enabled === true) await modelsQuery.refetch();
  }

  const models = modelsQuery.data;
  const llmEnabled = llmStatus.data?.enabled === true;
  const canSwitchModel = llmEnabled
    && models?.available === true
    && models.models.includes(selectedModel)
    && selectedModel !== models.selected_model
    && !modelMutation.isPending;

  return (
    <section className="mx-auto w-full max-w-5xl space-y-6 py-6" aria-labelledby="admin-heading">
      <header className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <p className="m-0 text-sm font-medium uppercase tracking-wide text-primary">{t('admin')}</p>
          <h1 id="admin-heading" className="m-0 text-3xl font-semibold text-on-surface">{t('adminLlmHeading')}</h1>
        </div>
        <Button type="button" variant="tonal" onClick={() => void refresh()}>{t('refresh')}</Button>
      </header>

      <section className="space-y-4 rounded-3xl bg-surface-container-low p-5 sm:p-6" aria-labelledby="admin-model-heading">
        <div>
          <h2 id="admin-model-heading" className="m-0 text-xl font-semibold text-on-surface">{t('llmModel')}</h2>
          <p className="m-0 mt-1 text-sm text-on-surface-variant">{t('llmModelDescription')}</p>
        </div>
        {llmStatus.isPending && <p role="status" className="m-0 text-on-surface-variant">{t('loadingLlmStatus')}</p>}
        {llmStatus.isError && <p role="alert" className="m-0 text-error">{errorMessage(llmStatus.error)}</p>}
        {llmStatus.data?.enabled === false && (
          <p role="status" className="m-0 rounded-2xl bg-surface-container p-4 text-on-surface-variant">{t('llmFeaturesDisabled')}</p>
        )}
        {llmEnabled && modelsQuery.isPending && <p role="status" className="m-0 text-on-surface-variant">{t('loadingLlmModels')}</p>}
        {llmEnabled && modelsQuery.isError && (
          <div role="alert" className="space-y-3 rounded-2xl bg-error-container p-4 text-on-error-container">
            <p className="m-0">{errorMessage(modelsQuery.error)}</p>
            <Button type="button" variant="tonal" onClick={() => void modelsQuery.refetch()}>{t('tryAgain')}</Button>
          </div>
        )}
        {llmEnabled && models && (
          <div className="space-y-3">
            {!models.available && (
              <p role="status" className="m-0 rounded-2xl bg-surface-container p-4 text-on-surface-variant">
                {models.warning || t('llmModelsUnavailable')}
              </p>
            )}
            {models.available && models.warning && (
              <p role="status" className="m-0 rounded-2xl bg-surface-container p-4 text-on-surface-variant">{models.warning}</p>
            )}
            <div className="flex flex-wrap items-end gap-3">
              <div className="min-w-60 flex-1 space-y-2">
                <label htmlFor="admin-llm-model" className="block text-sm font-medium text-on-surface">{t('selectedLlmModel')}</label>
                <select
                  id="admin-llm-model"
                  value={selectedModel}
                  disabled={!models.available || models.models.length === 0 || modelMutation.isPending}
                  onChange={(event) => setSelectedModel(event.target.value)}
                  className="min-h-12 w-full rounded-xl border border-outline bg-surface-container-lowest px-4 text-base text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary disabled:opacity-60"
                >
                  {!models.models.includes(models.selected_model) && models.selected_model && (
                    <option value={models.selected_model} disabled>{models.selected_model}</option>
                  )}
                  {models.models.map((model) => <option key={model} value={model}>{model}</option>)}
                </select>
              </div>
              <Button
                type="button"
                disabled={!canSwitchModel}
                onClick={() => modelMutation.mutate(selectedModel)}
              >
                {t(modelMutation.isPending ? 'savingLlmModel' : 'saveLlmModel')}
              </Button>
            </div>
            {modelMutation.isError && <p role="alert" className="m-0 text-error">{errorMessage(modelMutation.error)}</p>}
            {modelMutation.isSuccess && <p role="status" className="m-0 text-on-surface-variant">{t('llmModelSaved')}</p>}
          </div>
        )}
      </section>

      <section className="space-y-4 rounded-3xl bg-surface-container-low p-5 sm:p-6" aria-labelledby="admin-usage-heading">
        <div>
          <h2 id="admin-usage-heading" className="m-0 text-xl font-semibold text-on-surface">{t('llmUsage')}</h2>
          <p className="m-0 mt-1 text-sm text-on-surface-variant">{t('llmUsageLifetimeHint')}</p>
        </div>
        {usageQuery.isPending && <p role="status" className="m-0 text-on-surface-variant">{t('loadingLlmUsage')}</p>}
        {usageQuery.isError && (
          <div role="alert" className="space-y-3 rounded-2xl bg-error-container p-4 text-on-error-container">
            <p className="m-0">{errorMessage(usageQuery.error)}</p>
            <Button type="button" variant="tonal" onClick={() => void usageQuery.refetch()}>{t('tryAgain')}</Button>
          </div>
        )}
        {usageQuery.data && usageQuery.data.users.every((user) => user.models.length === 0) && (
          <p className="m-0 text-on-surface-variant">{t('noLlmUsage')}</p>
        )}
        {usageQuery.data && usageQuery.data.users.some((user) => user.models.length > 0) && (
          <div className="overflow-x-auto rounded-2xl border border-outline-variant">
            <table className="w-full min-w-[760px] border-collapse text-left text-sm text-on-surface">
              <caption className="sr-only">{t('llmUsageTable')}</caption>
              <thead className="bg-surface-container-high text-on-surface-variant">
                <tr>
                  {(['user', 'usageEmail', 'model', 'featureKind', 'requests', 'cachedRequests', 'promptTokens', 'completionTokens'] as const).map((key) => (
                    <th key={key} scope="col" className="px-3 py-3 font-medium">{t(key)}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {usageQuery.data.users.flatMap((user) => user.models.map((usage, index) => (
                  <tr key={`${user.user_id}-${usage.model}-${usage.kind}`} className="border-t border-outline-variant">
                    <td className="px-3 py-3">{index === 0 ? user.name : ''}</td>
                    <td className="px-3 py-3">{index === 0 ? user.email : ''}</td>
                    <td className="px-3 py-3">{usage.model}</td>
                    <td className="px-3 py-3">{usage.kind}</td>
                    <td className="px-3 py-3">{usage.requests.toLocaleString('en')}</td>
                    <td className="px-3 py-3">{usage.cached_requests.toLocaleString('en')}</td>
                    <td className="px-3 py-3">{usage.prompt_tokens.toLocaleString('en')}</td>
                    <td className="px-3 py-3">{usage.completion_tokens.toLocaleString('en')}</td>
                  </tr>
                )))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </section>
  );
}
