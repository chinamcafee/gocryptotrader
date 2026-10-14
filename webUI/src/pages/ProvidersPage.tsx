import { useEffect, useState } from 'react'
import { Alert, Button, Card, Col, Descriptions, Drawer, Form, Input, InputNumber, Modal, Row, Select, Space, Spin, Switch, Table, Tag, Typography, message } from 'antd'
import {
  errorText,
  useAddLlmVaultSecretMutation, useGetActiveModelPairQuery, useGetLlmProvidersQuery, useGetLlmStatusQuery, useGetLlmVaultQuery,
  useInitLlmVaultMutation, useSwitchActiveModelPairMutation, useTestLlmProviderProfileMutation, useUpdateLlmProviderProfileMutation,
  type LlmProviderProfile, type LlmProbeLayerResult, type LlmTestProviderProfileResponse, type LlmVaultEntry,
} from '@/app/api/gctApi'
import { NotImplemented } from '@/components/NotImplemented'

const { Paragraph, Text } = Typography

const KIND_LABEL: Record<string, string> = {
  LLM_OPENAI_COMPAT: 'LLM（OpenAI 兼容）',
  JEV: 'Jev（System One）',
}

/** 编辑表单字段契约（docs/llm-dev/05 §2.2 / 06 §8），W4-02 表单按此实现 */
const LLM_FIELDS = [
  ['baseUrl', 'Base URL', '必须 https（本地开发允许 http+127.0.0.1）'],
  ['modelId', '模型 ID', '如 deepseek-chat'],
  ['apiKeyRef', '密钥引用', 'secret://llm/<uuid>，不存明文'],
  ['apiKeyFingerprint', '指纹', 'SHA-256 截断，界面只显示指纹'],
  ['timeoutSeconds', '超时', '1-120s；实时分析钳到 ≤30s'],
  ['maxOutputTokens', '最大输出', '默认 1024（512 会截断）'],
  ['maxToolRounds / maxToolCalls', '工具上限', '默认 4 轮 / 8 次'],
  ['maxCumulativeTokens', '累计 token', '默认 24000'],
  ['promptVersion', '提示词版本', 'analysis-readonly-v1 等'],
  ['dailyBudgetUsd', '成本计划值', 'null 未设 / 0 禁止 / 正数=计划值（非硬闸）'],
]

const JEV_FIELDS = [
  ['baseUrl', 'System One URL', '默认 https://api.typesafe.ai'],
  ['modelId', '模型 ID', '必须匹配 ^jev-\\d+\\.\\d+\\.\\d+$，拒绝 jev-latest'],
  ['apiKeyRef', '密钥引用', 'secret://llm/<uuid>'],
  ['questionTemplateVersion', '问题模板版本', 'jev-review-v2'],
  ['reservationCap24h', '24h 预约上限', 'min(配置, 100)'],
]

/** 模型与配对（W4-01 档案列表 + W4-02 编辑表单 + W4-04 配对横幅的数据面；M1-14 起为真后端） */
export default function ProvidersPage() {
  const [includeArchived, setIncludeArchived] = useState(false)
  const [editing, setEditing] = useState<LlmProviderProfile>()
  const [saving, setSaving] = useState(false)
  const [probe, setProbe] = useState<{ profile: LlmProviderProfile; loading: boolean; result?: LlmTestProviderProfileResponse }>()
  const [form] = Form.useForm()
  const status = useGetLlmStatusQuery(undefined, { pollingInterval: 30_000 })
  const providers = useGetLlmProvidersQuery({ includeArchived }, { pollingInterval: 30_000 })
  const pair = useGetActiveModelPairQuery(undefined, { pollingInterval: 30_000 })
  const [updateProfile] = useUpdateLlmProviderProfileMutation()
  const [testProfile] = useTestLlmProviderProfileMutation()
  const { data: vault, error: vaultError } = useGetLlmVaultQuery()
  const [initVault] = useInitLlmVaultMutation()
  const [addSecret] = useAddLlmVaultSecretMutation()
  const [initializing, setInitializing] = useState(false)
  const [addingSecret, setAddingSecret] = useState(false)
  const [lastSecret, setLastSecret] = useState<{ ref: string; fingerprint: string }>()
  const [vaultForm] = Form.useForm()

  const onInitVault = async () => {
    try {
      setInitializing(true)
      await initVault().unwrap()
      message.success('vault 已就绪（已存在则原样打开——钥匙永不重铸）')
    } catch (err) {
      if (err && typeof err === 'object' && 'status' in (err as object)) message.error(errorText(err))
    } finally {
      setInitializing(false)
    }
  }

  const onAddSecret = async () => {
    try {
      const v = await vaultForm.validateFields()
      setAddingSecret(true)
      const out = await addSecret({ kind: v.kind, secret: v.secret }).unwrap()
      setLastSecret(out)
      vaultForm.setFieldsValue({ secret: '' })
      message.success('密钥已录入（明文不回显）')
    } catch (err) {
      if (err && typeof err === 'object' && 'status' in (err as object)) message.error(errorText(err))
    } finally {
      setAddingSecret(false)
    }
  }
  const [switchProfile] = useSwitchActiveModelPairMutation()
  const [switching, setSwitching] = useState(false)
  const [savingSwitch, setSavingSwitch] = useState(false)
  const [switchForm] = Form.useForm()

  // 切换配对（W4-05）：口令（重认证）+ 读到的 epoch（CAS）+ 幂等键，缺一不可
  const openSwitch = () => {
    switchForm.setFieldsValue({
      llmRevisionId: pair.data?.llmProfileRevisionId,
      jevRevisionId: pair.data?.jevProfileRevisionId,
      expectedEpoch: pair.data?.pairEpoch ?? 0,
      operatorPassword: '',
      actor: 'webui-operator',
      idempotencyKey: globalThis.crypto?.randomUUID?.() ?? `switch-${Date.now()}`,
    })
    setSwitching(true)
  }

  const onSwitch = async () => {
    try {
      const v = await switchForm.validateFields()
      setSavingSwitch(true)
      const result = await switchProfile({
        llmProfileRevisionId: v.llmRevisionId,
        jevProfileRevisionId: v.jevRevisionId,
        expectedPairEpoch: v.expectedEpoch,
        operatorPassword: v.operatorPassword,
        idempotencyKey: v.idempotencyKey,
        actor: v.actor,
      }).unwrap()
      message.success(`配对已切换：pair_epoch=${result.pairEpoch}，readiness=${result.readiness ?? '—'}`)
      setSwitching(false)
    } catch (err) {
      if (err && typeof err === 'object' && 'status' in (err as object)) {
        message.error(errorText(err))
      }
    } finally {
      setSavingSwitch(false)
    }
  }

  // live 修订版选项（未归档档案的最新修订版）——切换只能指向 live 版本
  const liveRevisions = (providers.data?.profiles ?? []).flatMap((p) => (p.revision ? [{ profile: p, revision: p.revision }] : []))
  const llmOptions = liveRevisions
    .filter((x) => x.profile.kind === 'LLM_OPENAI_COMPAT')
    .map((x) => ({ value: x.revision.id, label: `${x.profile.displayName} · #${x.revision.revisionNo} · ${x.revision.modelId}` }))
  const jevOptions = liveRevisions
    .filter((x) => x.profile.kind === 'JEV')
    .map((x) => ({ value: x.revision.id, label: `${x.profile.displayName} · #${x.revision.revisionNo} · ${x.revision.modelId}` }))

  // 探针 = 真招：判决会落库并影响 readiness（W4-03）
  const onProbe = async (profile: LlmProviderProfile) => {
    setProbe({ profile, loading: true })
    try {
      const result = await testProfile({ id: profile.id }).unwrap()
      setProbe({ profile, loading: false, result })
    } catch (err) {
      setProbe(undefined)
      if (err && typeof err === 'object' && 'status' in (err as object)) {
        message.error(errorText(err))
      }
    }
  }

  // 打开抽屉 = 把最新修订版预填进表单（未回显任何密钥；只回显引用与指纹）
  useEffect(() => {
    if (!editing) return
    const revision = editing.revision
    form.setFieldsValue({
      actor: 'webui-operator',
      baseUrl: revision?.baseUrl ?? '',
      modelId: revision?.modelId ?? '',
      apiKeyRef: revision?.apiKeyRef ?? '',
      apiKeyFingerprint: revision?.apiKeyFingerprint ?? '',
      timeoutSeconds: revision?.timeoutSeconds ?? 30,
      maxOutputTokens: revision?.maxOutputTokens,
      promptVersion: revision?.promptVersion,
      questionTemplateVersion: revision?.questionTemplateVersion,
      dailyBudgetUsd: revision?.dailyBudgetUsd,
      reservationCap24h: revision?.reservationCap24h,
      allowInsecureLocalhost: revision?.allowInsecureLocalhost ?? false,
      proxyAddress: revision?.proxyAddress,
    })
  }, [editing, form])

  const onSave = async () => {
    if (!editing) return
    try {
      const values = await form.validateFields()
      setSaving(true)
      await updateProfile({
        id: editing.id,
        actor: values.actor,
        baseUrl: values.baseUrl,
        modelId: values.modelId,
        apiKeyRef: values.apiKeyRef,
        apiKeyFingerprint: values.apiKeyFingerprint,
        timeoutSeconds: values.timeoutSeconds,
        maxOutputTokens: values.maxOutputTokens ?? undefined,
        promptVersion: values.promptVersion || undefined,
        questionTemplateVersion: values.questionTemplateVersion || undefined,
        dailyBudgetUsd: values.dailyBudgetUsd || undefined,
        reservationCap24h: values.reservationCap24h ?? undefined,
        allowInsecureLocalhost: values.allowInsecureLocalhost ?? false,
        proxyAddress: values.proxyAddress || undefined,
      }).unwrap()
      message.success('已追加新修订版（旧版成为不可变历史）')
      setEditing(undefined)
    } catch (err) {
      if (err && typeof err === 'object' && 'status' in (err as object)) {
        message.error(errorText(err))
      }
      // 表单校验失败：antd 已在字段上展示
    } finally {
      setSaving(false)
    }
  }

  return (
    <Row gutter={[12, 12]}>
      <Col span={24}>
        <Alert
          type="info"
          showIcon
          message="当前 LLM/Jev 组合（配对）"
          description={
            status.data ? (
              <Descriptions size="small" column={4}>
                <Descriptions.Item label="运行模式"><Tag color="blue">{status.data.runMode}</Tag></Descriptions.Item>
                <Descriptions.Item label="配对状态">
                  <Tag color={status.data.pairReadiness === 'READY' ? 'green' : 'orange'}>{status.data.pairReadiness}</Tag>
                </Descriptions.Item>
                <Descriptions.Item label="pair_epoch">{status.data.pairEpoch}</Descriptions.Item>
                <Descriptions.Item label="LIGHT/LIVE">
                  <Tag color={status.data.liveUnlocked ? 'red' : 'default'}>{status.data.liveUnlocked ? 'LIVE 已解锁' : '未解锁 LIVE'}</Tag>
                </Descriptions.Item>
                <Descriptions.Item label="LLM">{status.data.llmProfileName ?? '—'} / {status.data.llmModel ?? '—'}</Descriptions.Item>
                <Descriptions.Item label="Jev">{status.data.jevProfileName ?? '—'} / {status.data.jevModel ?? '—'}</Descriptions.Item>
              </Descriptions>
            ) : (
              <Paragraph style={{ marginBottom: 0 }}>
                切换配对的语义（docs/llm-dev/05 §2.4）：编辑活跃档案 → 生成新修订版 → 清空 active 指针 →
                <code>pair_epoch++</code> → 暂停运行中策略 → 撤销有效审批。模型回答不能自行延长授权、增加市场、改变限额或切换账户。
              </Paragraph>
            )
          }
        />
      </Col>

      <Col span={24}>
        <Card
          size="small"
          title={<span className="gct-panel-title">活化配对（llm/activepair · CAS 单行）</span>}
          extra={(
            <Space size={8}>
              <Button size="small" disabled={!pair.data?.found} onClick={openSwitch}>切换配对</Button>
              <Switch
              size="small"
              checkedChildren="含归档"
              unCheckedChildren="活动档案"
              checked={includeArchived}
              onChange={setIncludeArchived}
              />
            </Space>
          )}
        >
          {pair.error ? (
            <NotImplemented title="活化配对读取" error={pair.error} />
          ) : pair.data ? (
            pair.data.found ? (
              <Descriptions size="small" column={4}>
                <Descriptions.Item label="pair_epoch">{pair.data.pairEpoch}</Descriptions.Item>
                <Descriptions.Item label="readiness">
                  <Tag color={pair.data.readiness === 'READY' ? 'green' : 'orange'}>{pair.data.readiness}</Tag>
                </Descriptions.Item>
                <Descriptions.Item label="LLM 修订版"><Text className="gct-mono" style={{ fontSize: 12 }}>{pair.data.llmProfileRevisionId ?? '—'}</Text></Descriptions.Item>
                <Descriptions.Item label="Jev 修订版"><Text className="gct-mono" style={{ fontSize: 12 }}>{pair.data.jevProfileRevisionId ?? '—'}</Text></Descriptions.Item>
                <Descriptions.Item label="更新于">{pair.data.updatedAt ?? '—'}</Descriptions.Item>
                <Descriptions.Item label="更新者">{pair.data.updatedBy ?? '—'}</Descriptions.Item>
              </Descriptions>
            ) : (
              <Text type="secondary">尚未激活过任何配对（found=false 是状态，不是错误）。</Text>
            )
          ) : (
            <Text type="secondary">读取中…</Text>
          )}
        </Card>
      </Col>

      <Col span={24}>
        <Card size="small" title={<span className="gct-panel-title">Provider 档案（llm/providers · M1-14）</span>}>
          {providers.error ? (
            <NotImplemented title="Provider 档案与探针" error={providers.error} />
          ) : (
            <Table<LlmProviderProfile>
              size="small"
              rowKey="id"
              loading={providers.isLoading}
              dataSource={providers.data?.profiles ?? []}
              pagination={false}
              expandable={{
                expandedRowRender: (profile) => (profile.revision ? (
                  <Descriptions size="small" column={3}>
                    <Descriptions.Item label="修订版">#{profile.revision.revisionNo}（{profile.revisionCount} 版历史）</Descriptions.Item>
                    <Descriptions.Item label="Base URL"><Text className="gct-mono" style={{ fontSize: 12 }}>{profile.revision.baseUrl}</Text></Descriptions.Item>
                    <Descriptions.Item label="模型"><Text className="gct-mono" style={{ fontSize: 12 }}>{profile.revision.modelId}</Text></Descriptions.Item>
                    <Descriptions.Item label="密钥引用"><Text className="gct-mono" style={{ fontSize: 12 }}>{profile.revision.apiKeyRef}</Text></Descriptions.Item>
                    <Descriptions.Item label="指纹"><Text className="gct-mono" style={{ fontSize: 12 }}>{profile.revision.apiKeyFingerprint}</Text></Descriptions.Item>
                    <Descriptions.Item label="超时">{profile.revision.timeoutSeconds}s</Descriptions.Item>
                    <Descriptions.Item label="最大输出">{profile.revision.maxOutputTokens ?? '默认'}</Descriptions.Item>
                    <Descriptions.Item label="提示词版本">{profile.revision.promptVersion ?? '—'}</Descriptions.Item>
                    <Descriptions.Item label="问题模板版本">{profile.revision.questionTemplateVersion ?? '—'}</Descriptions.Item>
                    <Descriptions.Item label="成本计划值">{profile.revision.dailyBudgetUsd ?? '未设'}</Descriptions.Item>
                    <Descriptions.Item label="24h 预约上限">{profile.revision.reservationCap24h ?? '—'}</Descriptions.Item>
                    <Descriptions.Item label="允许本地 HTTP">{profile.revision.allowInsecureLocalhost ? '是（仅限回环）' : '否'}</Descriptions.Item>
                    <Descriptions.Item label="建立于">{profile.revision.createdAt}（{profile.revision.createdBy}）</Descriptions.Item>
                  </Descriptions>
                ) : (
                  <Text type="secondary">还没有任何修订版：档案创建后需要追加一个修订版才可用于配对。</Text>
                )),
              }}
              columns={[
                {
                  title: '显示名', dataIndex: 'displayName',
                  render: (v: string, p) => (
                    <span>
                      {v}
                      {p.archivedAt ? <Tag style={{ marginLeft: 6 }}>已归档</Tag> : null}
                    </span>
                  ),
                },
                {
                  title: '类型', dataIndex: 'kind', width: 200,
                  render: (v: string) => <Tag color={v === 'JEV' ? 'purple' : 'geekblue'}>{KIND_LABEL[v] ?? v}</Tag>,
                },
                {
                  title: '最新修订版', width: 130,
                  render: (_, p) => (p.revision
                    ? <span>#{p.revision.revisionNo}（共 {p.revisionCount} 版）</span>
                    : <Text type="secondary">无修订版</Text>),
                },
                {
                  title: '模型 / Base URL', width: 360,
                  render: (_, p) => (p.revision ? (
                    <span>
                      <Text className="gct-mono" style={{ fontSize: 12 }}>{p.revision.modelId}</Text>
                      <br />
                      <span className="gct-dim" style={{ fontSize: 11 }}>{p.revision.baseUrl}</span>
                    </span>
                  ) : '—'),
                },
                {
                  title: '指纹', width: 180,
                  render: (_, p) => (p.revision
                    ? <Text className="gct-mono" style={{ fontSize: 12 }}>{p.revision.apiKeyFingerprint}</Text>
                    : '—'),
                },
                {
                  title: '状态', width: 110,
                  render: (_, p) => (
                    <Tag color={p.enabled ? 'green' : 'default'}>{p.enabled ? '启用' : '停用'}</Tag>
                  ),
                },
                {
                  title: '操作', width: 150,
                  render: (_, p) => (
                    <Space size={4}>
                      <Button size="small" disabled={!!p.archivedAt} onClick={() => setEditing(p)}>
                        编辑
                      </Button>
                      <Button size="small" disabled={!!p.archivedAt} onClick={() => void onProbe(p)}>
                        探针
                      </Button>
                    </Space>
                  ),
                },
              ]}
            />
          )}
          <Paragraph className="gct-dim" style={{ fontSize: 11, marginTop: 8, marginBottom: 0 }}>
            删除永远是归档——修订版是不可变历史，行从不移除；密钥只以引用 + 指纹出现，明文从不经过前端。
            探针与编辑表单见 W4-02/W4-03（后端 `llm providers test` / `update` 已活，写操作受代理 <code>GCT_ALLOW_WRITES</code> 闸门约束）。
          </Paragraph>
        </Card>
      </Col>

      <Col span={24}>
        <Card
          size="small"
          title={<span className="gct-panel-title">密钥保管（vault · 09 §5，W4-06）</span>}
          extra={vault && !vault.initialized ? (
            <Button size="small" type="primary" loading={initializing} onClick={() => void onInitVault()}>初始化 vault</Button>
          ) : null}
        >
          {vaultError ? <NotImplemented title="密钥保管" error={vaultError} /> : vault ? (
            <>
              <Descriptions size="small" column={2}>
                <Descriptions.Item label="状态">
                  {vault.initialized ? <Tag color="green">已初始化</Tag> : <Tag color="orange">未初始化</Tag>}
                </Descriptions.Item>
                <Descriptions.Item label="目录">
                  <Text className="gct-mono" style={{ fontSize: 12 }}>{vault.directory ?? '—'}</Text>
                </Descriptions.Item>
              </Descriptions>
              {vault.initialized && (
                <>
                  <Table<LlmVaultEntry>
                    size="small" rowKey="ref" pagination={false}
                    dataSource={vault.entries ?? []}
                    columns={[
                      { title: '引用', dataIndex: 'ref', render: (v: string) => <Text className="gct-mono" style={{ fontSize: 11 }}>{v}</Text> },
                      { title: '类别', dataIndex: 'kind', width: 80 },
                      { title: '指纹', dataIndex: 'fingerprint', width: 180, render: (v?: string) => <Text className="gct-mono" style={{ fontSize: 11 }}>{v ?? '—'}</Text> },
                      { title: '建立于', dataIndex: 'createdAt', width: 190 },
                    ]}
                    locale={{ emptyText: <span className="gct-dim">还没有任何密钥</span> }}
                  />
                  <Form form={vaultForm} layout="inline" size="small" style={{ marginTop: 8, rowGap: 8 }}>
                    <Form.Item name="kind" rules={[{ required: true, message: '选择类别' }]}>
                      <Select style={{ width: 110 }} placeholder="类别" options={[{ value: 'llm', label: 'llm' }, { value: 'jev', label: 'jev' }]} />
                    </Form.Item>
                    <Form.Item name="secret" rules={[{ required: true, message: '输入密钥' }]}>
                      <Input.Password style={{ width: 320 }} placeholder="密钥明文（write-only，不回显）" autoComplete="new-password" />
                    </Form.Item>
                    <Form.Item>
                      <Button loading={addingSecret} onClick={() => void onAddSecret()}>录入密钥</Button>
                    </Form.Item>
                  </Form>
                  {lastSecret && (
                    <Alert
                      type="success" showIcon style={{ marginTop: 8 }}
                      message={`已录入：${lastSecret.ref}`}
                      description={`指纹 ${lastSecret.fingerprint}——把「引用 + 指纹」填进档案修订版（明文不落库、不回显，可核对不泄密）。`}
                    />
                  )}
                </>
              )}
            </>
          ) : <Text type="secondary">读取中…</Text>}
          <Paragraph className="gct-dim" style={{ fontSize: 11, marginTop: 8, marginBottom: 0 }}>
            密钥保管纪律（09 §5）：创建后<b>永不重新生成</b>（再次初始化只会原样打开——不会铸新钥匙、不会丢旧密钥）；明文只经一次录入、只落 RSA 包裹的密文；
            界面从此只见「引用 + 指纹」。写操作受代理 <code>GCT_ALLOW_WRITES</code> 闸门。
          </Paragraph>
        </Card>
      </Col>

      <Col xs={24} lg={12}>
        <Card size="small" title={<span className="gct-panel-title">LLM 档案编辑表单契约（W4-02）</span>}>
          <Table
            size="small" pagination={false}
            dataSource={LLM_FIELDS.map(([k, name, note]) => ({ key: k, k, name, note }))}
            columns={[
              { title: '字段', dataIndex: 'k', width: 190, render: (v: string) => <Text className="gct-mono" style={{ fontSize: 12 }}>{v}</Text> },
              { title: '说明', dataIndex: 'name', width: 110 },
              { title: '', dataIndex: 'note', render: (v: string) => <span className="gct-dim">{v}</span> },
            ]}
          />
          <Paragraph className="gct-dim" style={{ fontSize: 11, marginTop: 8, marginBottom: 0 }}>
            四层探针：① 最小 chat + 认证 ② 只读函数工具往返 ③ 完整 Candidate 协议（合成无数据场景必须返回 HOLD）
            ④ 对话流式。结果落库并影响 readiness。
          </Paragraph>
        </Card>
      </Col>

      <Col xs={24} lg={12}>
        <Card size="small" title={<span className="gct-panel-title">Jev 档案编辑表单契约（W4-02）</span>}>
          <Table
            size="small" pagination={false}
            dataSource={JEV_FIELDS.map(([k, name, note]) => ({ key: k, k, name, note }))}
            columns={[
              { title: '字段', dataIndex: 'k', width: 190, render: (v: string) => <Text className="gct-mono" style={{ fontSize: 12 }}>{v}</Text> },
              { title: '说明', dataIndex: 'name', width: 110 },
              { title: '', dataIndex: 'note', render: (v: string) => <span className="gct-dim">{v}</span> },
            ]}
          />
          <Paragraph className="gct-dim" style={{ fontSize: 11, marginTop: 8, marginBottom: 0 }}>
            <Text strong>Jev 必需、无降级路径</Text>（Q3 定案）：无 Jev 则 <code>pair_readiness != READY</code>，
            一切自动决策停止。Jev 只回答有界问题（Choice / Score / Noul），不取数据、不下单，
            其概率不得换算为收益或胜率。
          </Paragraph>
        </Card>
      </Col>

      <Drawer
        title={`编辑 Provider 档案：${editing?.displayName ?? ''}`}
        width={560}
        open={!!editing}
        onClose={() => setEditing(undefined)}
        forceRender
        extra={(
          <Space>
            <Button onClick={() => setEditing(undefined)}>取消</Button>
            <Button type="primary" loading={saving} onClick={() => void onSave()}>提交（追加新修订版）</Button>
          </Space>
        )}
      >
        <Alert
          type="warning" showIcon style={{ marginBottom: 12 }}
          message="提交 = 追加新修订版，不是就地修改"
          description="修订版不可变；若被取代的版本正是活化配对指向的版本，配对指针会被清空、pair_epoch++、readiness 转 INCOMPLETE——直到重新激活配对。密钥录入在 vault（W4-06）：这里只填引用与指纹。"
        />
        <Form form={form} layout="vertical" size="small">
          <Form.Item name="actor" label="操作者（审计留痕）" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item name="baseUrl" label="Base URL" rules={[{ required: true }]}>
            <Input placeholder="https://api.example.com/v1（本地 http 仅限回环 + 显式豁免）" />
          </Form.Item>
          <Form.Item name="modelId" label="模型 ID" rules={[{ required: true }]}>
            <Input placeholder={editing?.kind === 'JEV' ? 'jev-1.0.0（必须打点版本）' : '如 deepseek-chat'} />
          </Form.Item>
          <Form.Item name="apiKeyRef" label="密钥引用（vault）" rules={[{ required: true }]}>
            <Input placeholder="secret://llm/<id>" />
          </Form.Item>
          <Form.Item name="apiKeyFingerprint" label="密钥指纹（16 hex，只核对不回显）" rules={[{ required: true, pattern: /^[0-9a-f]{16}$/, message: '需 16 位小写 hex' }]}>
            <Input />
          </Form.Item>
          <Form.Item name="timeoutSeconds" label="超时（秒）" rules={[{ required: true }]}>
            <InputNumber min={1} max={120} style={{ width: '100%' }} />
          </Form.Item>
          {editing?.kind === 'JEV' ? (
            <Form.Item name="questionTemplateVersion" label="问题模板版本（Jev）" rules={[{ required: true }]}>
              <Input placeholder="jev-review-v2" />
            </Form.Item>
          ) : (
            <>
              <Form.Item name="maxOutputTokens" label="最大输出 token（LLM，留空=默认）">
                <InputNumber min={1} style={{ width: '100%' }} />
              </Form.Item>
              <Form.Item name="promptVersion" label="提示词版本（LLM，留空=默认）">
                <Input placeholder="如 analysis-readonly-v1" />
              </Form.Item>
            </>
          )}
          <Form.Item name="dailyBudgetUsd" label="成本计划值（空=未设 / 0=禁止花钱 / 正数=计划值；非硬闸）">
            <Input placeholder="如 5.00" />
          </Form.Item>
          <Form.Item name="reservationCap24h" label="24h 预约上限">
            <InputNumber min={0} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="allowInsecureLocalhost" label="允许本地 HTTP（仅回环）" valuePropName="checked">
            <Switch />
          </Form.Item>
          <Form.Item name="proxyAddress" label="出站代理（留空=直连）">
            <Input placeholder="http://127.0.0.1:7890" />
          </Form.Item>
        </Form>
      </Drawer>

      <Modal
        title={`分层探针：${probe?.profile.displayName ?? ''}`}
        open={!!probe}
        onCancel={() => setProbe(undefined)}
        footer={<Button onClick={() => setProbe(undefined)}>关闭</Button>}
        width={760}
      >
        {probe?.loading ? (
          <div style={{ padding: 32, textAlign: 'center' }}><Spin tip="正在跑探针（会真实请求供应商）…" /></div>
        ) : probe?.result ? (
          <>
            <Paragraph className="gct-dim" style={{ fontSize: 12 }}>
              修订版 <Text className="gct-mono" style={{ fontSize: 12 }}>{probe.result.revisionId}</Text>：每层独立判决——AUTH 失败是密钥问题、TOOL_CALL 失败是函数调用能力缺失、
              SCHEMA 失败是模型在无数据场景编造交易、STREAM 失败是对话流式不可用。<Text strong>判决已落库并影响 readiness</Text>。
            </Paragraph>
            <Table<LlmProbeLayerResult>
              size="small"
              rowKey="layer"
              pagination={false}
              dataSource={probe.result.results ?? []}
              columns={[
                { title: '层', dataIndex: 'layer', width: 110, render: (v: string) => <Text className="gct-mono" style={{ fontSize: 12 }}>{v}</Text> },
                { title: '判决', dataIndex: 'passed', width: 80, render: (v: boolean) => <Tag color={v ? 'green' : 'red'}>{v ? 'PASS' : 'FAIL'}</Tag> },
                { title: '错误码', dataIndex: 'errorCode', width: 200, render: (v?: string) => v ? <Text className="gct-mono" style={{ fontSize: 11 }}>{v}</Text> : '—' },
                { title: '延迟', dataIndex: 'latencyMs', width: 80, render: (v?: number) => (v != null ? `${v}ms` : '—') },
                { title: 'tokens', width: 100, render: (_, r) => `${r.inputTokens ?? 0}/${r.outputTokens ?? 0}` },
                { title: '返回模型', dataIndex: 'responseModel', render: (v?: string) => <span className="gct-dim" style={{ fontSize: 11 }}>{v ?? '—'}</span> },
              ]}
              expandable={{
                expandedRowRender: (r) => (
                  <Paragraph className="gct-dim" style={{ fontSize: 12, marginBottom: 0 }}>
                    {r.errorMessage || '（该层无错误信息）'}
                  </Paragraph>
                ),
                rowExpandable: (r) => !!r.errorMessage,
              }}
            />
          </>
        ) : null}
      </Modal>

      <Modal
        title="切换活化配对（CAS + 重认证）"
        open={switching}
        onCancel={() => setSwitching(false)}
        footer={(
          <Space>
            <Button onClick={() => setSwitching(false)}>取消</Button>
            <Button type="primary" loading={savingSwitch} onClick={() => void onSwitch()}>提交切换</Button>
          </Space>
        )}
        width={640}
      >
        <Alert
          type="warning" showIcon style={{ marginBottom: 12 }}
          message="切换会改写全局决策模型（两侧一起换）"
          description="激活成功后 pair_epoch+1；旧配对下运行中的策略将暂停、其有效审批将被撤销（影响面登记在切换命令里）。请确认两侧修订版都通过了探针（readiness 由最新探针判决推导）。"
        />
        <Form form={switchForm} layout="vertical" size="small">
          <Form.Item name="llmRevisionId" label="LLM 修订版（live）" rules={[{ required: true }]}>
            <Select options={llmOptions} placeholder="选择 LLM 档案的 live 修订版" />
          </Form.Item>
          <Form.Item name="jevRevisionId" label="Jev 修订版（live）" rules={[{ required: true }]}>
            <Select options={jevOptions} placeholder="选择 Jev 档案的 live 修订版" />
          </Form.Item>
          <Form.Item name="expectedEpoch" label="expected_pair_epoch（你读到的那一版）" rules={[{ required: true }]}>
            <InputNumber style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="operatorPassword" label="操作者口令（重认证）" rules={[{ required: true }]}>
            <Input.Password />
          </Form.Item>
          <Form.Item name="idempotencyKey" label="幂等键（重试安全：同键同参=同一次切换）" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item name="actor" label="操作者（审计留痕）" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
        </Form>
      </Modal>
    </Row>
  )
}
