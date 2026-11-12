import { defineStore } from "pinia";
import { ref } from "vue";
import { getSystemStatus, type SystemStatus } from "../lib/api";
export const useSystem = defineStore("system", () => {
  const status = ref<SystemStatus | null>(null),
    loading = ref(false),
    failed = ref(false);
  async function refresh() {
    if (loading.value) return;
    loading.value = true;
    failed.value = false;
    try {
      status.value = await getSystemStatus();
    } catch {
      failed.value = true;
      status.value = null;
    } finally {
      loading.value = false;
    }
  }
  return { status, loading, failed, refresh };
});
