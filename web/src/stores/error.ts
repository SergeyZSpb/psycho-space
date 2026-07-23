import { defineStore } from 'pinia';
import { ref } from 'vue';
import { ApiError } from '../api/client';

// Drives the single global error modal mounted at the app root. Any unexpected
// failure (network, 5xx, unexpected auth loss) is funnelled here so the user
// always sees the trace id to send to the admin.
export const useErrorStore = defineStore('error', () => {
  const open = ref(false);
  const code = ref('');
  const status = ref(0);
  const traceId = ref('');

  function report(err: unknown) {
    if (err instanceof ApiError) {
      code.value = err.code;
      status.value = err.status;
      traceId.value = err.traceId;
    } else {
      code.value = 'unexpected';
      status.value = 0;
      traceId.value = '';
    }
    open.value = true;
  }

  function close() {
    open.value = false;
  }

  return { open, code, status, traceId, report, close };
});
