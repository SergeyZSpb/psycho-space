<template>
  <div :class="inline ? 'd-flex align-center ga-1' : 'd-flex flex-column align-center'">
    <v-btn
      :color="voted ? 'primary' : undefined"
      :variant="voted ? 'flat' : buttonVariant"
      icon="mdi-arrow-up-bold"
      :size="size"
      :loading="loading"
      :title="label"
      :aria-label="label"
      @click="emit('toggle')"
    />
    <span class="font-weight-bold" :class="[countClass, inline ? '' : 'mt-1']">{{ votes }}</span>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';

const props = withDefaults(
  defineProps<{
    votes: number;
    voted: boolean;
    loading?: boolean;
    size?: string;
    inline?: boolean;
  }>(),
  { loading: false, size: 'small', inline: false },
);

const emit = defineEmits<{ toggle: [] }>();

// State-reflecting label: retracting a cast vote vs. casting one.
const label = computed(() => (props.voted ? 'Убрать голос' : 'Голос'));
const countClass = computed(() => (props.size === 'x-small' ? 'text-body-2' : 'text-subtitle-1'));
// Inline (messenger footer) reads better as a flat text button when unvoted.
const buttonVariant = computed(() => (props.inline ? 'text' : 'tonal'));
</script>
