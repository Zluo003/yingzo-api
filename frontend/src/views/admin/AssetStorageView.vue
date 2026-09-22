<template>
  <div class="space-y-6">
    <!-- Usage summary -->
    <div class="card p-6">
      <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
        <div>
          <h3 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('admin.assetStorage.usage.title') }}
          </h3>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.assetStorage.usage.description') }}
          </p>
          <p class="mt-1 text-xs text-gray-400 dark:text-gray-500">
            {{ t('admin.assetStorage.sourceLabel', { source: sourceLabel }) }}
          </p>
        </div>
        <button
          type="button"
          class="btn btn-secondary btn-sm"
          :disabled="loading"
          data-testid="asset-storage-refresh"
          @click="loadSettings"
        >
          {{ loading ? t('common.loading') : t('common.refresh') }}
        </button>
      </div>

      <div class="space-y-4">
        <!-- Per-category usage, each category against its own budget -->
        <div>
          <h4 class="mb-2 text-xs font-medium tracking-wide text-gray-400 uppercase dark:text-gray-500">
            {{ t('admin.assetStorage.usage.byCategory') }}
          </h4>
          <div class="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
            <div class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
              <div class="text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.assetStorage.usage.referenceFiles') }}
              </div>
              <div
                class="mt-1 text-lg font-semibold text-gray-900 dark:text-white"
                data-testid="asset-storage-usage-reference-files"
              >
                {{ formatCount(usage.reference_files) }}
              </div>
            </div>
            <div class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
              <div class="text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.assetStorage.usage.referenceBytes') }}
              </div>
              <div
                class="mt-1 text-lg font-semibold text-gray-900 dark:text-white"
                data-testid="asset-storage-usage-reference-bytes"
              >
                {{ formatSize(usage.reference_bytes) }}
              </div>
              <div
                class="mt-1 text-xs text-gray-500 dark:text-gray-400"
                data-testid="asset-storage-usage-reference-budget"
              >
                {{ budgetText(usage.reference_bytes, referenceBudget) }}
              </div>
            </div>
            <div class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
              <div class="text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.assetStorage.usage.generatedFiles') }}
              </div>
              <div
                class="mt-1 text-lg font-semibold text-gray-900 dark:text-white"
                data-testid="asset-storage-usage-generated-files"
              >
                {{ formatCount(usage.generated_files) }}
              </div>
            </div>
            <div class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
              <div class="text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.assetStorage.usage.generatedBytes') }}
              </div>
              <div
                class="mt-1 text-lg font-semibold text-gray-900 dark:text-white"
                data-testid="asset-storage-usage-generated-bytes"
              >
                {{ formatSize(usage.generated_bytes) }}
              </div>
              <div
                class="mt-1 text-xs text-gray-500 dark:text-gray-400"
                data-testid="asset-storage-usage-generated-budget"
              >
                {{ budgetText(usage.generated_bytes, generatedBudget) }}
              </div>
            </div>
          </div>
        </div>

        <!-- Totals across both categories -->
        <div>
          <h4 class="mb-2 text-xs font-medium tracking-wide text-gray-400 uppercase dark:text-gray-500">
            {{ t('admin.assetStorage.usage.totals') }}
          </h4>
          <div class="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
            <div class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
              <div class="text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.assetStorage.usage.activeFiles') }}
              </div>
              <div
                class="mt-1 text-lg font-semibold text-gray-900 dark:text-white"
                data-testid="asset-storage-usage-active-files"
              >
                {{ formatCount(usage.active_files) }}
              </div>
            </div>
            <div class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
              <div class="text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.assetStorage.usage.activeBytes') }}
              </div>
              <div
                class="mt-1 text-lg font-semibold text-gray-900 dark:text-white"
                data-testid="asset-storage-usage-active-bytes"
              >
                {{ formatSize(usage.active_bytes) }}
              </div>
            </div>
            <div class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
              <div class="text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.assetStorage.usage.localFiles') }}
              </div>
              <div
                class="mt-1 text-lg font-semibold text-gray-900 dark:text-white"
                data-testid="asset-storage-usage-local-files"
              >
                {{ formatCount(usage.local_files) }}
              </div>
            </div>
            <div class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
              <div class="text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.assetStorage.usage.s3Files') }}
              </div>
              <div
                class="mt-1 text-lg font-semibold text-gray-900 dark:text-white"
                data-testid="asset-storage-usage-s3-files"
              >
                {{ formatCount(usage.s3_files) }}
              </div>
            </div>
            <div class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
              <div class="text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.assetStorage.usage.expiringWithin1Hour') }}
              </div>
              <div
                class="mt-1 text-lg font-semibold text-gray-900 dark:text-white"
                data-testid="asset-storage-usage-expiring"
              >
                {{ formatCount(usage.expiring_within_1_hour) }}
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <div
      v-if="loading && !settings"
      class="card flex items-center gap-2 p-6 text-sm text-gray-500 dark:text-gray-400"
    >
      <div class="h-4 w-4 animate-spin rounded-full border-b-2 border-primary-600"></div>
      {{ t('common.loading') }}
    </div>

    <template v-else>
      <!-- Shared storage backend + reference material settings -->
      <div class="card p-6">
        <div class="mb-4">
          <h3 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('admin.assetStorage.title') }}
          </h3>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.assetStorage.description') }}
          </p>
        </div>

        <!-- Backend -->
        <div>
          <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
            {{ t('admin.assetStorage.backend.label') }}
          </label>
          <div class="flex flex-wrap gap-4">
            <label class="inline-flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
              <input
                v-model="form.backend"
                type="radio"
                value="local"
                data-testid="asset-storage-backend-local"
              />
              <span>{{ t('admin.assetStorage.backend.local') }}</span>
            </label>
            <label class="inline-flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
              <input
                v-model="form.backend"
                type="radio"
                value="s3"
                data-testid="asset-storage-backend-s3"
              />
              <span>{{ t('admin.assetStorage.backend.s3') }}</span>
            </label>
          </div>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{
              form.backend === 's3'
                ? t('admin.assetStorage.backend.s3Hint')
                : t('admin.assetStorage.backend.localHint')
            }}
          </p>

          <!-- 本地素材目录：可配置，Docker 部署时应指向宿主机 bind mount 进来的真实目录 -->
          <div v-if="form.backend === 'local'" class="mt-3">
            <label
              class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400"
              for="asset-storage-local-dir"
            >
              {{ t('admin.assetStorage.backend.localDirLabel') }}
            </label>
            <input
              id="asset-storage-local-dir"
              v-model="form.local_dir"
              type="text"
              class="input w-full"
              data-testid="asset-storage-local-dir"
              :placeholder="localDirPlaceholder"
            />
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.assetStorage.backend.localDirHint') }}
            </p>
            <p
              v-if="localPath"
              class="mt-1 text-xs text-gray-400 dark:text-gray-500"
              data-testid="asset-storage-local-path"
            >
              {{ t('admin.assetStorage.backend.localDirEffective', { path: localPath }) }}
            </p>
            <p
              v-if="mountedHostDir"
              class="mt-1 text-xs text-gray-400 dark:text-gray-500"
              data-testid="asset-storage-mounted-host-dir"
            >
              {{
                t('admin.assetStorage.backend.mountedHostDir', {
                  host: mountedHostDir,
                  container: localPath,
                })
              }}
            </p>
          </div>
        </div>

        <!-- S3 credentials (s3 backend only) -->
        <div
          v-if="form.backend === 's3'"
          class="mt-4 grid grid-cols-1 gap-3 md:grid-cols-2"
          data-testid="asset-storage-s3-fields"
        >
          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
              {{ t('admin.assetStorage.s3.endpoint') }}
            </label>
            <input
              v-model="form.s3.endpoint"
              class="input w-full"
              placeholder="https://<account_id>.r2.cloudflarestorage.com"
            />
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
              {{ t('admin.assetStorage.s3.region') }}
            </label>
            <input v-model="form.s3.region" class="input w-full" placeholder="auto" />
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
              {{ t('admin.assetStorage.s3.bucket') }}
            </label>
            <input
              v-model="form.s3.bucket"
              class="input w-full"
              data-testid="asset-storage-s3-bucket"
            />
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
              {{ t('admin.assetStorage.s3.prefix') }}
            </label>
            <input v-model="form.s3.prefix" class="input w-full" placeholder="model-assets/" />
          </div>
          <div class="md:col-span-2" data-testid="asset-storage-s3-custom-domain">
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
              {{ t('admin.assetStorage.s3.customDomain') }}
            </label>
            <input
              v-model="form.s3.custom_domain"
              class="input w-full"
              placeholder="https://cdn.example.com"
            />
            <p class="mt-1 text-xs text-gray-400 dark:text-gray-500">
              {{ t('admin.assetStorage.s3.customDomainHint') }}
            </p>
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
              {{ t('admin.assetStorage.s3.accessKeyId') }}
            </label>
            <input
              v-model="form.s3.access_key_id"
              class="input w-full"
              data-testid="asset-storage-s3-access-key-id"
            />
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
              {{ t('admin.assetStorage.s3.secretAccessKey') }}
            </label>
            <input
              v-model="form.s3.secret_access_key"
              type="password"
              class="input w-full"
              data-testid="asset-storage-s3-secret-access-key"
              :placeholder="
                secretAccessKeyConfigured ? t('admin.assetStorage.s3.secretConfigured') : ''
              "
            />
          </div>
          <label
            class="inline-flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300 md:col-span-2"
          >
            <input v-model="form.s3.force_path_style" type="checkbox" />
            <span>{{ t('admin.assetStorage.s3.forcePathStyle') }}</span>
          </label>
        </div>

        <!-- Public base URL -->
        <div class="mt-6 border-t border-gray-100 pt-4 dark:border-dark-700">
          <h4 class="text-sm font-medium text-gray-900 dark:text-white">
            {{ t('admin.assetStorage.publicBaseUrl.title') }}
          </h4>
          <div class="mt-3">
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
              {{ t('admin.assetStorage.publicBaseUrl.label') }}
            </label>
            <input
              v-model="form.public_base_url"
              class="input w-full"
              data-testid="asset-storage-public-base-url"
              :placeholder="effectivePublicBaseUrl"
            />
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.assetStorage.publicBaseUrl.hint') }}
            </p>
            <p class="mt-1 text-xs text-gray-400 dark:text-gray-500">
              {{
                t('admin.assetStorage.publicBaseUrl.effective', {
                  url: effectivePublicBaseUrl,
                })
              }}
            </p>
          </div>
        </div>

        <!-- Capacity reserve waterline: shared by both storage categories -->
        <div
          class="mt-6 border-t border-gray-100 pt-4 dark:border-dark-700"
          data-testid="asset-storage-capacity-reserve"
        >
          <h4 class="text-sm font-medium text-gray-900 dark:text-white">
            {{ t('admin.assetStorage.capacityReserve.title') }}
          </h4>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.assetStorage.capacityReserve.description') }}
          </p>
          <div class="mt-3 grid grid-cols-1 gap-3 md:grid-cols-2">
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
                {{ t('admin.assetStorage.capacityReserve.label') }}
              </label>
              <input
                v-model.number="form.capacity_reserve_percent"
                type="number"
                min="0"
                max="50"
                step="1"
                class="input w-full"
                data-testid="asset-storage-capacity-reserve-percent"
              />
              <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.assetStorage.capacityReserve.hint') }}
              </p>
            </div>
            <!-- 用当前输入的上限与冗余比例实时算出水位 -->
            <div
              class="rounded-lg border border-gray-200 p-3 text-xs text-gray-500 dark:border-dark-600 dark:text-gray-400"
            >
              <p data-testid="asset-storage-capacity-reserve-hint">
                {{ referenceWaterlineHint }}
              </p>
              <p class="mt-1" data-testid="asset-storage-result-capacity-reserve-hint">
                {{ resultWaterlineHint }}
              </p>
            </div>
          </div>
        </div>

        <!-- Reference material retention, capacity and quota -->
        <div class="mt-6 border-t border-gray-100 pt-4 dark:border-dark-700">
          <h4 class="text-sm font-medium text-gray-900 dark:text-white">
            {{ t('admin.assetStorage.reference.title') }}
          </h4>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.assetStorage.reference.description') }}
          </p>
          <div class="mt-3 grid grid-cols-1 gap-3 md:grid-cols-2">
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
                {{ t('admin.assetStorage.reference.retentionHours') }}
              </label>
              <input
                v-model.number="form.retention_hours"
                type="number"
                min="1"
                max="720"
                class="input w-full"
                data-testid="asset-storage-retention-hours"
              />
              <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.assetStorage.reference.retentionHoursHint') }}
              </p>
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
                {{ t('admin.assetStorage.reference.maxTotalBytes') }}
              </label>
              <div class="flex items-center gap-2">
                <input
                  v-model.number="maxTotalBytesValue"
                  type="number"
                  min="0"
                  step="any"
                  class="input w-full"
                  data-testid="asset-storage-max-total-bytes"
                />
                <select
                  v-model="maxTotalBytesUnit"
                  class="input w-28"
                  data-testid="asset-storage-max-total-bytes-unit"
                >
                  <option value="MiB">{{ t('admin.assetStorage.byteUnit.mib') }}</option>
                  <option value="GiB">{{ t('admin.assetStorage.byteUnit.gib') }}</option>
                </select>
              </div>
              <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                {{
                  form.max_total_bytes > 0
                    ? t('admin.assetStorage.reference.maxTotalBytesHint', {
                        bytes: formatCount(form.max_total_bytes),
                      })
                    : t('admin.assetStorage.reference.maxTotalBytesUnlimited')
                }}
              </p>
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
                {{ t('admin.assetStorage.reference.dailyMaxCount') }}
              </label>
              <input
                v-model.number="form.daily_max_count"
                type="number"
                min="1"
                max="1000000"
                class="input w-full"
                data-testid="asset-storage-daily-max-count"
              />
              <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.assetStorage.reference.dailyMaxCountHint') }}
              </p>
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
                {{ t('admin.assetStorage.reference.dailyMaxBytes') }}
              </label>
              <div class="flex items-center gap-2">
                <input
                  v-model.number="dailyMaxBytesValue"
                  type="number"
                  min="0"
                  step="any"
                  class="input w-full"
                  data-testid="asset-storage-daily-max-bytes"
                />
                <select
                  v-model="dailyMaxBytesUnit"
                  class="input w-28"
                  data-testid="asset-storage-daily-max-bytes-unit"
                >
                  <option value="MiB">{{ t('admin.assetStorage.byteUnit.mib') }}</option>
                  <option value="GiB">{{ t('admin.assetStorage.byteUnit.gib') }}</option>
                </select>
              </div>
              <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                {{
                  t('admin.assetStorage.reference.byteHint', {
                    bytes: formatCount(form.daily_max_bytes),
                  })
                }}
              </p>
            </div>
          </div>
        </div>
      </div>

      <!-- Generated result retention and capacity -->
      <div class="card p-6" data-testid="asset-storage-generated-card">
        <div class="mb-4">
          <h3 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('admin.assetStorage.generated.title') }}
          </h3>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.assetStorage.generated.description') }}
          </p>
        </div>

        <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
              {{ t('admin.assetStorage.generated.resultRetentionHours') }}
            </label>
            <input
              v-model.number="form.result_retention_hours"
              type="number"
              min="1"
              max="8760"
              class="input w-full"
              data-testid="asset-storage-result-retention-hours"
            />
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.assetStorage.generated.resultRetentionHoursHint') }}
            </p>
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
              {{ t('admin.assetStorage.generated.resultMaxTotalBytes') }}
            </label>
            <div class="flex items-center gap-2">
              <input
                v-model.number="resultMaxTotalBytesValue"
                type="number"
                min="0"
                step="any"
                class="input w-full"
                data-testid="asset-storage-result-max-total-bytes"
              />
              <select
                v-model="resultMaxTotalBytesUnit"
                class="input w-28"
                data-testid="asset-storage-result-max-total-bytes-unit"
              >
                <option value="MiB">{{ t('admin.assetStorage.byteUnit.mib') }}</option>
                <option value="GiB">{{ t('admin.assetStorage.byteUnit.gib') }}</option>
              </select>
            </div>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{
                form.result_max_total_bytes > 0
                  ? t('admin.assetStorage.generated.resultMaxTotalBytesHint', {
                      bytes: formatCount(form.result_max_total_bytes),
                    })
                  : t('admin.assetStorage.generated.resultMaxTotalBytesUnlimited')
              }}
            </p>
          </div>
        </div>

        <!-- 生成产物自己的每日配额：与下游上传的参考素材上传配额互相独立 -->
        <div class="mt-6 border-t border-gray-100 pt-4 dark:border-dark-700">
          <h4 class="text-sm font-medium text-gray-900 dark:text-white">
            {{ t('admin.assetStorage.generated.dailyQuotaTitle') }}
          </h4>
          <p
            class="mt-1 text-xs text-gray-500 dark:text-gray-400"
            data-testid="asset-storage-result-daily-quota-hint"
          >
            {{ t('admin.assetStorage.generated.dailyQuotaDescription') }}
          </p>
          <div class="mt-3 grid grid-cols-1 gap-3 md:grid-cols-2">
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
                {{ t('admin.assetStorage.generated.resultDailyMaxCount') }}
              </label>
              <input
                v-model.number="form.result_daily_max_count"
                type="number"
                min="0"
                max="1000000"
                step="1"
                class="input w-full"
                data-testid="asset-storage-result-daily-max-count"
              />
              <p
                class="mt-1 text-xs text-gray-500 dark:text-gray-400"
                data-testid="asset-storage-result-daily-max-count-hint"
              >
                {{ t('admin.assetStorage.generated.resultDailyMaxCountHint') }}
              </p>
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">
                {{ t('admin.assetStorage.generated.resultDailyMaxBytes') }}
              </label>
              <div class="flex items-center gap-2">
                <input
                  v-model.number="resultDailyMaxBytesValue"
                  type="number"
                  min="0"
                  step="any"
                  class="input w-full"
                  data-testid="asset-storage-result-daily-max-bytes"
                />
                <select
                  v-model="resultDailyMaxBytesUnit"
                  class="input w-28"
                  data-testid="asset-storage-result-daily-max-bytes-unit"
                >
                  <option value="MiB">{{ t('admin.assetStorage.byteUnit.mib') }}</option>
                  <option value="GiB">{{ t('admin.assetStorage.byteUnit.gib') }}</option>
                </select>
              </div>
              <p
                class="mt-1 text-xs text-gray-500 dark:text-gray-400"
                data-testid="asset-storage-result-daily-max-bytes-hint"
              >
                {{
                  form.result_daily_max_bytes > 0
                    ? t('admin.assetStorage.generated.resultDailyMaxBytesHint', {
                        bytes: formatCount(form.result_daily_max_bytes),
                      })
                    : t('admin.assetStorage.generated.resultDailyMaxBytesUnlimited')
                }}
              </p>
            </div>
          </div>
        </div>
      </div>

      <!-- Actions -->
      <div class="card flex flex-wrap gap-2 p-4">
        <button
          type="button"
          class="btn btn-secondary btn-sm"
          :disabled="testing"
          data-testid="asset-storage-test"
          @click="testConnection"
        >
          {{ testing ? t('common.loading') : t('admin.assetStorage.testConnection') }}
        </button>
        <button
          type="button"
          class="btn btn-primary btn-sm"
          :disabled="saving"
          data-testid="asset-storage-save"
          @click="saveSettings"
        >
          {{ saving ? t('common.saving') : t('common.save') }}
        </button>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api'
import { useAppStore } from '@/stores'
import { formatBytes, formatNumberLocaleString } from '@/utils/format'
import type {
  FileStorageBackend,
  FileStorageConfig,
  FileStorageSettings,
  FileStorageUsage,
} from '@/api/admin/fileStorage'

const { t } = useI18n()
const appStore = useAppStore()

const MIB = 1024 * 1024
const GIB = 1024 * MIB
const MAX_RETENTION_HOURS = 720
/** 生成产物的保存时长上限为一年，比参考素材宽松。 */
const MAX_RESULT_RETENTION_HOURS = 8760
const MAX_DAILY_COUNT = 1_000_000
/** 容量冗余水位：0 表示不留冗余（占用达到上限才开始清理），后端允许的上限是 50。 */
const DEFAULT_CAPACITY_RESERVE_PERCENT = 10
const MAX_CAPACITY_RESERVE_PERCENT = 50

type ByteUnit = 'MiB' | 'GiB'
const byteUnitFactors: Record<ByteUnit, number> = { MiB: MIB, GiB: GIB }

const EMPTY_USAGE: FileStorageUsage = {
  active_files: 0,
  active_bytes: 0,
  local_files: 0,
  s3_files: 0,
  expiring_within_1_hour: 0,
  reference_files: 0,
  reference_bytes: 0,
  generated_files: 0,
  generated_bytes: 0,
}

function emptyConfig(): FileStorageConfig {
  return {
    schema_version: 1,
    backend: 'local',
    // 空值 = 使用默认目录（<data_dir>/agent-assets），与后端的默认行为一致
    local_dir: '',
    public_base_url: '',
    retention_hours: 24,
    daily_max_count: 100,
    daily_max_bytes: 2 * GIB,
    max_total_bytes: 0,
    result_retention_hours: 24,
    result_max_total_bytes: 0,
    // 生成产物的每日配额默认 0 = 不限制，与后端的默认值一致：交付给下游的产物不会
    // 因为配额被拒绝，需要防刷时再由管理员显式填写。
    result_daily_max_count: 0,
    result_daily_max_bytes: 0,
    capacity_reserve_percent: DEFAULT_CAPACITY_RESERVE_PERCENT,
    s3: {
      endpoint: '',
      region: 'auto',
      bucket: '',
      access_key_id: '',
      secret_access_key: '',
      prefix: 'model-assets/',
      custom_domain: '',
      force_path_style: false,
    },
  }
}

const form = ref<FileStorageConfig>(emptyConfig())
const settings = ref<FileStorageSettings | null>(null)
const loading = ref(false)
const saving = ref(false)
const testing = ref(false)

// 字节量与展示单位分开保存：表单里始终是"数值 + 单位"，提交时才换算为字节。
const maxTotalBytesUnit = ref<ByteUnit>('GiB')
const dailyMaxBytesUnit = ref<ByteUnit>('MiB')
const resultMaxTotalBytesUnit = ref<ByteUnit>('MiB')
const resultDailyMaxBytesUnit = ref<ByteUnit>('MiB')

function bytesToUnitValue(bytes: number, unit: ByteUnit): number {
  if (!Number.isFinite(bytes) || bytes <= 0) return 0
  return Number((bytes / byteUnitFactors[unit]).toFixed(6))
}

function unitValueToBytes(raw: unknown, unit: ByteUnit): number {
  const value = typeof raw === 'number' ? raw : Number(raw)
  if (!Number.isFinite(value) || value <= 0) return 0
  return Math.round(value * byteUnitFactors[unit])
}

function preferredByteUnit(bytes: number): ByteUnit {
  return bytes > 0 && bytes % GIB === 0 ? 'GiB' : 'MiB'
}

/**
 * 归一化容量冗余水位：后端始终返回 0–50 的数字；缺失或越界（更早版本的配置）时回落到
 * 默认值，避免表单出现空值。
 */
function normalizeReservePercent(raw: unknown): number {
  return typeof raw === 'number' &&
    Number.isInteger(raw) &&
    raw >= 0 &&
    raw <= MAX_CAPACITY_RESERVE_PERCENT
    ? raw
    : DEFAULT_CAPACITY_RESERVE_PERCENT
}

/**
 * 归一化生成产物的每日数量配额：后端始终返回 0–1000000 的整数（0 = 不限制）；
 * 字段缺失（更早版本的配置）或越界时回落到 0，保持"产物默认不会被配额拒绝"的语义。
 */
function normalizeResultDailyCount(raw: unknown): number {
  return typeof raw === 'number' && Number.isInteger(raw) && raw >= 0 && raw <= MAX_DAILY_COUNT
    ? raw
    : 0
}

/** 归一化生成产物的每日字节配额：缺失或非法时回落到 0（不限制）。 */
function normalizeResultDailyBytes(raw: unknown): number {
  return typeof raw === 'number' && Number.isFinite(raw) && raw >= 0 ? raw : 0
}

const maxTotalBytesValue = computed({
  get: () => bytesToUnitValue(form.value.max_total_bytes, maxTotalBytesUnit.value),
  set: (value: unknown) => {
    form.value.max_total_bytes = unitValueToBytes(value, maxTotalBytesUnit.value)
  },
})

const dailyMaxBytesValue = computed({
  get: () => bytesToUnitValue(form.value.daily_max_bytes, dailyMaxBytesUnit.value),
  set: (value: unknown) => {
    form.value.daily_max_bytes = unitValueToBytes(value, dailyMaxBytesUnit.value)
  },
})

const resultMaxTotalBytesValue = computed({
  get: () => bytesToUnitValue(form.value.result_max_total_bytes, resultMaxTotalBytesUnit.value),
  set: (value: unknown) => {
    form.value.result_max_total_bytes = unitValueToBytes(value, resultMaxTotalBytesUnit.value)
  },
})

const resultDailyMaxBytesValue = computed({
  get: () => bytesToUnitValue(form.value.result_daily_max_bytes, resultDailyMaxBytesUnit.value),
  set: (value: unknown) => {
    form.value.result_daily_max_bytes = unitValueToBytes(value, resultDailyMaxBytesUnit.value)
  },
})

const usage = computed<FileStorageUsage>(() => settings.value?.usage ?? EMPTY_USAGE)
const localPath = computed(() => settings.value?.local_path || '')
// 占位符用当前生效的目录，让"留空 = 使用默认目录"一眼可读；拿不到生效值时退回到文字说明。
const mountedHostDir = computed(() => settings.value?.mounted_host_dir ?? '')

const localDirPlaceholder = computed(
  () => localPath.value || t('admin.assetStorage.backend.localDirPlaceholder'),
)
// 用量按类别展示时对照已保存的预算，避免未保存的输入影响读数。
const referenceBudget = computed(() => Number(settings.value?.max_total_bytes) || 0)
const generatedBudget = computed(() => Number(settings.value?.result_max_total_bytes) || 0)
const secretAccessKeyConfigured = computed(
  () => settings.value?.secret_access_key_configured ?? false,
)
const effectivePublicBaseUrl = computed(
  () => settings.value?.effective_public_base_url || '-',
)
const sourceLabel = computed(() => {
  const source = settings.value?.source || ''
  const known = ['database', 'environment', 'default']
  return known.includes(source)
    ? t(`admin.assetStorage.source.${source}`)
    : t('admin.assetStorage.source.unknown')
})

function formatCount(value: number): string {
  return formatNumberLocaleString(Number.isFinite(value) ? value : 0)
}

function formatSize(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return t('admin.assetStorage.zeroBytes')
  return formatBytes(value, 1)
}

/** 按类别展示"已用 / 预算"，未设置预算（0）时明确说明不限制。 */
function budgetText(usedBytes: number, budgetBytes: number): string {
  return budgetBytes > 0
    ? t('admin.assetStorage.usage.budgetUsage', {
        used: formatSize(usedBytes),
        budget: formatSize(budgetBytes),
      })
    : t('admin.assetStorage.usage.budgetUnlimited', { used: formatSize(usedBytes) })
}

/** 水位提示用当前输入的冗余比例；输入暂时非法时回落到默认值，保证提示始终可读。 */
const reservePercentForHint = computed(() =>
  normalizeReservePercent(form.value.capacity_reserve_percent),
)

/** 与上方的容量输入保持一致，用当前选中的 MiB/GiB 单位展示水位数值。 */
function formatWaterlineSize(bytes: number, unit: ByteUnit): string {
  const value = Number((bytes / byteUnitFactors[unit]).toFixed(2))
  const unitLabel =
    unit === 'GiB' ? t('admin.assetStorage.byteUnit.gib') : t('admin.assetStorage.byteUnit.mib')
  return `${formatCount(value)} ${unitLabel}`
}

/**
 * 实时水位提示：占用超过 `上限 × (1 − 冗余比例)` 就开始按最早失效优先清理。
 * 参考素材与生成产物各用自己的上限单独计算；上限为 0（不限制）时该类不触发清理。
 */
function capacityReserveHint(
  category: 'reference' | 'generated',
  capBytes: number,
  unit: ByteUnit,
): string {
  const cap = Number(capBytes)
  if (!Number.isFinite(cap) || cap <= 0) {
    return t(`admin.assetStorage.capacityReserve.${category}Unlimited`)
  }
  const reserve = reservePercentForHint.value
  return t(`admin.assetStorage.capacityReserve.${category}Hint`, {
    cap: formatWaterlineSize(cap, unit),
    reserve: formatCount(reserve),
    threshold: formatWaterlineSize(cap * (1 - reserve / 100), unit),
  })
}

const referenceWaterlineHint = computed(() =>
  capacityReserveHint('reference', form.value.max_total_bytes, maxTotalBytesUnit.value),
)
const resultWaterlineHint = computed(() =>
  capacityReserveHint('generated', form.value.result_max_total_bytes, resultMaxTotalBytesUnit.value),
)

function applySettings(data: FileStorageSettings): void {
  const base = emptyConfig()
  form.value = {
    ...base,
    ...data,
    schema_version: data.schema_version || base.schema_version,
    // 更早版本的配置里没有 local_dir：缺失时按空值（默认目录）处理，而不是 undefined
    local_dir: typeof data.local_dir === 'string' ? data.local_dir : '',
    capacity_reserve_percent: normalizeReservePercent(data.capacity_reserve_percent),
    // 产物配额缺失/非法时按 0（不限制）处理，绝不把交付物挡在配额之外
    result_daily_max_count: normalizeResultDailyCount(data.result_daily_max_count),
    result_daily_max_bytes: normalizeResultDailyBytes(data.result_daily_max_bytes),
    s3: { ...base.s3, ...data.s3, secret_access_key: '', custom_domain: typeof data.s3?.custom_domain === 'string' ? data.s3.custom_domain : '' },
  }
  maxTotalBytesUnit.value = preferredByteUnit(form.value.max_total_bytes)
  dailyMaxBytesUnit.value = preferredByteUnit(form.value.daily_max_bytes)
  resultMaxTotalBytesUnit.value = preferredByteUnit(form.value.result_max_total_bytes)
  resultDailyMaxBytesUnit.value = preferredByteUnit(form.value.result_daily_max_bytes)
}

function isAcceptablePublicBaseUrl(raw: string): boolean {
  let parsed: URL
  try {
    parsed = new URL(raw)
  } catch {
    return false
  }
  if (parsed.protocol === 'https:') return true
  if (parsed.protocol !== 'http:') return false
  const host = parsed.hostname.replace(/^\[|\]$/g, '')
  return host === 'localhost' || host === '127.0.0.1' || host === '::1'
}

/**
 * 与后端 blockedFileStorageDirs 保持一致：把素材根目录指到这些地方只会破坏系统。
 */
const BLOCKED_LOCAL_DIRS = new Set([
  '/bin',
  '/sbin',
  '/lib',
  '/lib64',
  '/usr',
  '/etc',
  '/proc',
  '/sys',
  '/dev',
  '/boot',
  '/root',
  '/var',
])

/**
 * 模拟后端的 filepath.Clean：折叠重复斜杠、去掉结尾斜杠与 `.`、解析 `..`，
 * 让 `/etc/`、`//etc`、`/var/../etc` 这些写法也被识别为同一个系统目录。
 */
function cleanLocalDir(raw: string): string {
  const segments: string[] = []
  for (const segment of raw.split('/')) {
    if (segment === '' || segment === '.') continue
    if (segment === '..') {
      segments.pop()
      continue
    }
    segments.push(segment)
  }
  return `/${segments.join('/')}`
}

/**
 * 校验本地素材目录，返回错误提示的 i18n key（合法时返回 null）。
 * 规则与后端 normalizeFileStorageLocalDir 保持一致：空值表示使用默认目录；否则必须是
 * 绝对路径、不能是文件系统根目录，也不能是系统目录。
 */
function localDirValidationError(raw: string): string | null {
  if (raw === '') return null
  if (!raw.startsWith('/')) return 'admin.assetStorage.validation.localDirAbsolute'
  const cleaned = cleanLocalDir(raw)
  if (cleaned === '/') return 'admin.assetStorage.validation.localDirRoot'
  if (BLOCKED_LOCAL_DIRS.has(cleaned)) return 'admin.assetStorage.validation.localDirSystem'
  return null
}

/**
 * 校验并归一化表单为后端 PUT/POST 的请求体；校验失败时提示并返回 null。
 * 规则与后端 normalizeFileStorageConfig 保持一致，避免把明显非法的值提交上去。
 */
function buildConfig(): FileStorageConfig | null {
  const retentionHours = Number(form.value.retention_hours)
  if (
    !Number.isInteger(retentionHours) ||
    retentionHours < 1 ||
    retentionHours > MAX_RETENTION_HOURS
  ) {
    appStore.showError(t('admin.assetStorage.validation.retentionHours'))
    return null
  }

  const dailyMaxCount = Number(form.value.daily_max_count)
  if (!Number.isInteger(dailyMaxCount) || dailyMaxCount < 1 || dailyMaxCount > MAX_DAILY_COUNT) {
    appStore.showError(t('admin.assetStorage.validation.dailyMaxCount'))
    return null
  }

  const dailyMaxBytes = Number(form.value.daily_max_bytes)
  if (!Number.isFinite(dailyMaxBytes) || dailyMaxBytes < 1) {
    appStore.showError(t('admin.assetStorage.validation.dailyMaxBytes'))
    return null
  }

  const maxTotalBytes = Number(form.value.max_total_bytes)
  if (!Number.isFinite(maxTotalBytes) || maxTotalBytes < 0) {
    appStore.showError(t('admin.assetStorage.validation.maxTotalBytes'))
    return null
  }

  const resultRetentionHours = Number(form.value.result_retention_hours)
  if (
    !Number.isInteger(resultRetentionHours) ||
    resultRetentionHours < 1 ||
    resultRetentionHours > MAX_RESULT_RETENTION_HOURS
  ) {
    appStore.showError(t('admin.assetStorage.validation.resultRetentionHours'))
    return null
  }

  const resultMaxTotalBytes = Number(form.value.result_max_total_bytes)
  if (!Number.isFinite(resultMaxTotalBytes) || resultMaxTotalBytes < 0) {
    appStore.showError(t('admin.assetStorage.validation.resultMaxTotalBytes'))
    return null
  }

  // 生成产物自己的每日配额：0 是合法值（不限制，默认），上限与参考素材配额一致。
  const resultDailyMaxCount = Number(form.value.result_daily_max_count)
  if (
    typeof form.value.result_daily_max_count !== 'number' ||
    !Number.isInteger(resultDailyMaxCount) ||
    resultDailyMaxCount < 0 ||
    resultDailyMaxCount > MAX_DAILY_COUNT
  ) {
    appStore.showError(t('admin.assetStorage.validation.resultDailyMaxCount'))
    return null
  }

  const resultDailyMaxBytes = Number(form.value.result_daily_max_bytes)
  if (!Number.isFinite(resultDailyMaxBytes) || resultDailyMaxBytes < 0) {
    appStore.showError(t('admin.assetStorage.validation.resultDailyMaxBytes'))
    return null
  }

  const capacityReservePercent = Number(form.value.capacity_reserve_percent)
  if (
    typeof form.value.capacity_reserve_percent !== 'number' ||
    !Number.isInteger(capacityReservePercent) ||
    capacityReservePercent < 0 ||
    capacityReservePercent > MAX_CAPACITY_RESERVE_PERCENT
  ) {
    appStore.showError(t('admin.assetStorage.validation.capacityReservePercent'))
    return null
  }

  // 本地素材目录：空值合法（使用默认目录），否则必须是绝对路径且不能是系统目录
  const localDir = (form.value.local_dir ?? '').trim()
  const localDirError = localDirValidationError(localDir)
  if (localDirError) {
    appStore.showError(t(localDirError))
    return null
  }

  const backend: FileStorageBackend = form.value.backend === 's3' ? 's3' : 'local'
  const s3 = {
    endpoint: form.value.s3.endpoint.trim(),
    region: form.value.s3.region.trim() || 'auto',
    bucket: form.value.s3.bucket.trim(),
    access_key_id: form.value.s3.access_key_id.trim(),
    secret_access_key: form.value.s3.secret_access_key ?? '',
    prefix: form.value.s3.prefix.trim() || 'model-assets/',
    custom_domain: (form.value.s3.custom_domain ?? '').trim(),
    force_path_style: Boolean(form.value.s3.force_path_style),
  }
  if (
    backend === 's3' &&
    (!s3.bucket ||
      !s3.access_key_id ||
      (!s3.secret_access_key && !secretAccessKeyConfigured.value))
  ) {
    appStore.showError(t('admin.assetStorage.validation.s3Required'))
    return null
  }
  // 自定义域名可选；填了就必须是合法的 scheme+host，与后端校验保持一致。
  if (backend === 's3' && s3.custom_domain && !isAcceptablePublicBaseUrl(s3.custom_domain)) {
    appStore.showError(t('admin.assetStorage.validation.customDomain'))
    return null
  }

  const publicBaseUrl = form.value.public_base_url.trim()
  if (publicBaseUrl && !isAcceptablePublicBaseUrl(publicBaseUrl)) {
    appStore.showError(t('admin.assetStorage.validation.publicBaseUrl'))
    return null
  }

  return {
    schema_version: form.value.schema_version || 1,
    backend,
    // 始终提交：空字符串表示使用默认目录，而不是"保持原值"
    local_dir: localDir,
    public_base_url: publicBaseUrl,
    retention_hours: retentionHours,
    daily_max_count: dailyMaxCount,
    daily_max_bytes: dailyMaxBytes,
    max_total_bytes: maxTotalBytes,
    result_retention_hours: resultRetentionHours,
    result_max_total_bytes: resultMaxTotalBytes,
    // 产物配额始终提交：0 表示不限制，与参考素材的上传配额互相独立
    result_daily_max_count: resultDailyMaxCount,
    result_daily_max_bytes: resultDailyMaxBytes,
    capacity_reserve_percent: capacityReservePercent,
    s3,
  }
}

async function loadSettings() {
  loading.value = true
  try {
    const data = await adminAPI.fileStorage.getFileStorageSettings()
    settings.value = data
    applySettings(data)
  } catch (error) {
    appStore.showError(
      (error as { message?: string })?.message || t('admin.assetStorage.loadFailed'),
    )
  } finally {
    loading.value = false
  }
}

async function saveSettings() {
  const config = buildConfig()
  if (!config) return
  saving.value = true
  try {
    const data = await adminAPI.fileStorage.updateFileStorageSettings(config)
    settings.value = data
    applySettings(data)
    appStore.showSuccess(t('admin.assetStorage.saved'))
  } catch (error) {
    appStore.showError(
      (error as { message?: string })?.message || t('admin.assetStorage.saveFailed'),
    )
  } finally {
    saving.value = false
  }
}

async function testConnection() {
  const config = buildConfig()
  if (!config) return
  testing.value = true
  try {
    const result = await adminAPI.fileStorage.testFileStorageSettings(config)
    if (result.ok) {
      appStore.showSuccess(result.message || t('admin.assetStorage.testSuccess'))
    } else {
      appStore.showError(result.message || t('admin.assetStorage.testFailed'))
    }
  } catch (error) {
    appStore.showError(
      (error as { message?: string })?.message || t('admin.assetStorage.testFailed'),
    )
  } finally {
    testing.value = false
  }
}

onMounted(loadSettings)
</script>
