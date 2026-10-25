<script setup>
import { computed } from 'vue'
import { formatTL, formatNumber } from '../store'

defineOptions({ name: 'EquipmentNode' })

const props = defineProps({
  node: { type: Object, required: true },
  depth: { type: Number, default: 0 },
  expandedIds: { type: Array, default: () => [] },
  selectedId: { type: Number, default: null }
})

defineEmits(['toggle', 'select'])

const hasChildren = computed(() => (props.node.children || []).length > 0)
const isOpen = computed(() => props.expandedIds.includes(props.node.id))
const isSelected = computed(() => props.selectedId === props.node.id)

const levelBadge = computed(() => {
  switch (props.node.level_type) {
    case 'tesis':
      return 'bg-indigo-100 text-indigo-700 border-indigo-200'
    case 'hat':
      return 'bg-blue-100 text-blue-700 border-blue-200'
    case 'makine':
      return 'bg-emerald-100 text-emerald-700 border-emerald-200'
    default:
      return 'bg-slate-100 text-slate-600 border-slate-200'
  }
})
</script>

<template>
  <div>
    <div
      class="group flex items-center gap-2 px-2 py-1.5 rounded-lg cursor-pointer border"
      :class="isSelected ? 'bg-emerald-50 border-emerald-300' : 'border-transparent hover:bg-slate-50'"
      :style="{ marginLeft: `${depth * 16}px` }"
      @click="$emit('select', node)"
    >
      <button
        v-if="hasChildren"
        class="w-5 h-5 shrink-0 flex items-center justify-center rounded text-slate-400 hover:text-slate-700 hover:bg-slate-200"
        @click.stop="$emit('toggle', node.id)"
        :aria-label="isOpen ? 'Daralt' : 'Genişlet'"
      >
        <span class="text-[10px] font-bold">{{ isOpen ? '▼' : '▶' }}</span>
      </button>
      <span v-else class="w-5 shrink-0 text-center text-[10px] text-slate-300">•</span>

      <span class="font-mono text-[11px] text-slate-500 shrink-0">{{ node.code }}</span>

      <span class="text-xs text-slate-800 truncate">{{ node.name }}</span>

      <span v-if="node.is_critical" class="text-[10px] px-1 py-0.5 rounded bg-red-100 text-red-700 font-bold shrink-0">KRİTİK</span>

      <span :class="['text-[10px] px-1.5 py-0.5 rounded border font-mono shrink-0', levelBadge]">{{ node.level_type }}</span>

      <span
        v-if="node.status !== 'aktif'"
        class="text-[10px] px-1.5 py-0.5 rounded bg-amber-100 text-amber-800 font-semibold shrink-0"
      >{{ node.status }}</span>

      <span class="ml-auto flex items-center gap-3 shrink-0 text-[10px] text-slate-400 font-mono">
        <span v-if="node.rollup_downtime_hours > 0" class="text-rose-600">
          {{ formatNumber(node.rollup_downtime_hours, 1) }} sa
        </span>
        <span v-if="node.rollup_cost_kurus > 0" class="text-slate-600">
          {{ formatTL(node.rollup_cost_kurus) }}
        </span>
      </span>
    </div>

    <div v-if="hasChildren && isOpen">
      <EquipmentNode
        v-for="child in node.children"
        :key="child.id"
        :node="child"
        :depth="depth + 1"
        :expanded-ids="expandedIds"
        :selected-id="selectedId"
        @toggle="$emit('toggle', $event)"
        @select="$emit('select', $event)"
      />
    </div>
  </div>
</template>
