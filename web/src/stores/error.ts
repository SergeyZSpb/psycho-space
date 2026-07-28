import { defineStore } from 'pinia';
import { ref } from 'vue';
import { ApiError } from '../api/client';

// Human-readable RU explanation for the error codes worth explaining. Anything
// not listed falls back to the modal's generic wording.
const FRIENDLY_MESSAGES: Record<string, string> = {
  // The AI answered, but with something the game can't use: either it garbled its
  // own reply beyond repair, or its content filter refused the topic. Either way
  // it is the model's problem, not the player's, and the turn is not lost.
  llm_unparsable:
    'ИИ выдал ответ, который игра не смогла разобрать — бывает. ' +
    'Попробуй выбрать другой вариант ответа, а этот код отправь Сергею, чтобы он проверил.',

  // The three refusals the admin screen can produce, spelled out because the
  // generic wording ("что-то пошло не так") reads as a bug when in fact the
  // server is refusing on purpose and the admin can act on the reason.
  already_forgotten: 'Этот пользователь уже забыт — обезличивать второй раз нечего.',
  cannot_modify_self: 'Себя нельзя — ни заблокировать, ни забыть.',
  cannot_modify_superadmin: 'Суперадмина трогать нельзя: это единственный аккаунт, который нельзя потерять.',
};

// Drives the single global error modal mounted at the app root. Any unexpected
// failure (network, 5xx, unexpected auth loss) is funnelled here so the user
// always sees the trace id to send to the admin.
export const useErrorStore = defineStore('error', () => {
  const open = ref(false);
  const code = ref('');
  const status = ref(0);
  const traceId = ref('');
  const message = ref(''); // friendly explanation; empty -> generic wording

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
    message.value = FRIENDLY_MESSAGES[code.value] ?? '';
    open.value = true;
  }

  function close() {
    open.value = false;
  }

  return { open, code, status, traceId, message, report, close };
});
