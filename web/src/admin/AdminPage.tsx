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
    <section className="mx-auto grid w-full min-w-0 max-w-5xl gap-6" aria-labelledby="admin-heading">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <header className="page-intro">
          <h1 id="admin-heading">{t('adminLlmHeading')}</h1>
        </header>
        <Button type="button" variant="tonal" onClick={() => void refresh()}>{t('refresh')}</Button>
      </div>

      <section className="grid min-w-0 gap-3 border-t border-outline-variant pt-4" aria-labelledby="admin-model-heading">
        <header>
          <h2 id="admin-model-heading" className="m-0 text-base font-semibold text-on-surface">{t('llmModel')}</h2>
          <p className="m-0 mt-1 text-sm text-on-surface-variant">{t('llmModelDescription')}</p>
        </header>
        {llmStatus.isPending && <p role="status" className="m-0 text-on-surface-variant">{t('loadingLlmStatus')}</p>}
        {llmStatus.isError && <p role="alert" className="m-0 text-error">{errorMessage(llmStatus.error)}</p>}
        {llmStatus.data?.enabled === false && (
          <p role="status" className="m-0 text-sm text-on-surface-variant">{t('llmFeaturesDisabled')}</p>
        )}
        {llmEnabled && modelsQuery.isPending && <p role="status" className="m-0 text-on-surface-variant">{t('loadingLlmModels')}</p>}
        {llmEnabled && modelsQuery.isError && (
          <div role="alert" className="space-y-3 rounded-2xl bg-error-container p-4 text-on-error-container">
            <p className="m-0">{errorMessage(modelsQuery.error)}</p>
            <Button type="button" variant="tonal" onClick={() => void modelsQuery.refetch()}>{t('tryAgain')}</Button>
          </div>
        )}
        {llmEnabled && models && (
          <div className="grid min-w-0 gap-3">
            {!models.available && (
              <p role="status" className="m-0 text-sm text-on-surface-variant">
                {models.warning || t('llmModelsUnavailable')}
              </p>
            )}
            {models.available && models.warning && (
              <p role="status" className="m-0 text-sm text-on-surface-variant">{models.warning}</p>
            )}
            <div className="flex max-w-2xl flex-wrap items-end gap-2">
              <div className="grid min-w-0 flex-1 basis-64 gap-1">
                <label htmlFor="admin-llm-model" className="block text-sm font-medium text-on-surface">{t('selectedLlmModel')}</label>
                <select
                  id="admin-llm-model"
                  value={selectedModel}
                  disabled={!models.available || models.models.length === 0 || modelMutation.isPending}
                  onChange={(event) => setSelectedModel(event.target.value)}
                  className="min-h-12 w-full min-w-0 rounded-xl border border-outline bg-surface-container-lowest px-3 text-base text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary disabled:opacity-60"
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

      <section className="grid min-w-0 gap-3 border-t border-outline-variant pt-4" aria-labelledby="admin-usage-heading">
        <header>
          <h2 id="admin-usage-heading" className="m-0 text-base font-semibold text-on-surface">{t('llmUsage')}</h2>
          <p className="m-0 mt-1 text-sm text-on-surface-variant">{t('llmUsageLifetimeHint')}</p>
        </header>
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
          <div role="region" aria-label={t('llmUsageTable')} tabIndex={0} className="overflow-x-auto rounded-xl focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary">
            <table className="w-full min-w-[44rem] border-collapse text-start text-sm text-on-surface">
              <caption className="sr-only">{t('llmUsageTable')}</caption>
              <thead className="bg-surface-container text-on-surface-variant">
                <tr>
                  {(['user', 'usageEmail', 'model', 'featureKind', 'requests', 'cachedRequests', 'promptTokens', 'completionTokens'] as const).map((key, index) => (
                    <th key={key} scope="col" className={`px-3 py-2 font-medium ${index >= 4 ? 'text-end' : ''}`}>{t(key)}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {usageQuery.data.users.flatMap((user) => user.models.map((usage, index) => (
                  <tr key={`${user.user_id}-${usage.model}-${usage.kind}`} className="border-t border-outline-variant">
                    <td className="px-3 py-2">{index === 0 ? user.name : ''}</td>
                    <td className="px-3 py-2">{index === 0 ? user.email : ''}</td>
                    <td className="px-3 py-2">{usage.model}</td>
                    <td className="px-3 py-2">{usage.kind}</td>
                    {[usage.requests, usage.cached_requests, usage.prompt_tokens, usage.completion_tokens].map((count, column) => (
                      <td key={column} className="px-3 py-2 text-end tabular-nums">{count.toLocaleString('en')}</td>
                    ))}
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
