import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { create } from '@bufbuild/protobuf'
import TaskPanel from '../components/TaskPanel.vue'
import { ChapterStatus } from '../gen/classicfarm/v1/ws/chapter/chapter_status_pb'
import { ChapterViewSchema, TaskProgressViewSchema } from '../gen/classicfarm/v1/ws/ws_pb'

function task(taskId: number, currentValue: number, completed: boolean) {
  return create(TaskProgressViewSchema, { taskId, currentValue, targetValue: 1, completed })
}

function chapter(chapterId: number, status: ChapterStatus, tasks = [] as ReturnType<typeof task>[]) {
  return create(ChapterViewSchema, { chapterId, status, tasks })
}

describe('task chapter panel', () => {
  it('pages between completed chapter one and the live named chapter two', async () => {
    const wrapper = mount(TaskPanel, {
      props: {
        connected: true,
        chapter: chapter(2, ChapterStatus.IN_PROGRESS, [
          task(6, 1, true),
          task(7, 0, false),
          task(8, 0, false),
        ]),
      },
    })

    expect(wrapper.text()).toContain('2 / 2')
    expect(wrapper.text()).toContain('添加 1 位好友')
    expect(wrapper.text()).toContain('偷取 1 次好友作物')
    expect(wrapper.text()).toContain('给好友农场投虫 1 次')

    await wrapper.find('.chapter-pages button').trigger('click')
    expect(wrapper.text()).toContain('1 / 2')
    expect(wrapper.text()).toContain('购买 3 粒种子')
    expect(wrapper.text()).toContain('已领取')
    expect(wrapper.text()).toContain('3 / 3')
  })

  it('shows exact rewards and terminal state for a claimed chapter two', () => {
    const wrapper = mount(TaskPanel, {
      props: {
        connected: true,
        chapter: chapter(2, ChapterStatus.CLAIMED, [
          task(6, 1, true),
          task(7, 1, true),
          task(8, 1, true),
        ]),
      },
    })

    expect(wrapper.text()).toContain('暂时没有更多任务了')
    expect(wrapper.find('button.primary').exists()).toBe(false)
  })

  it('offers the configured reward for each claimable chapter', async () => {
    const wrapper = mount(TaskPanel, {
      props: {
        connected: true,
        chapter: chapter(1, ChapterStatus.CLAIMABLE),
      },
    })
    expect(wrapper.find('button.primary').text()).toContain('3 个南瓜种子')

    await wrapper.setProps({
      chapter: chapter(2, ChapterStatus.CLAIMABLE),
    })
    expect(wrapper.find('button.primary').text()).toContain('10 金币、5 个肥料、10 个西瓜种子')
  })
})
