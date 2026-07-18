import { useEffect, useState } from "react";
import {
  Card, Input, Button, Typography, Tag, Spin, Empty, Progress, Alert,
  Space, Switch, message, Tree, Modal, Form, Select, Tooltip, Row, Col, Divider,
} from "antd";
import {
  RobotOutlined, ThunderboltOutlined, CheckCircleFilled,
  BranchesOutlined, ApiOutlined, PlusOutlined, EditOutlined, DeleteOutlined,
  ReloadOutlined, BulbOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";

const { Text, Title } = Typography;
const { TextArea } = Input;

interface Provider { id: string; name: string; }
interface IntentNode {
  id: string; intent_code: string; parent_code?: string | null;
  name: string; description: string; level: number;
  examples: string[]; provider_id?: string | null; enabled: boolean;
  children?: IntentNode[];
}
interface Candidate {
  intent_code: string; name: string; description: string;
  score: number; reason: string;
  provider_id?: string | null; provider_name?: string | null;
}
interface ClassifyResult {
  question: string; candidates: Candidate[];
  top: Candidate[]; matched: Candidate | null;
  default_provider?: { found: boolean; id?: string; name?: string };
  switched?: { success: boolean; provider_name?: string; detail?: string; fallback?: boolean };
}

const API = "http://localhost:15722";

export default function IntentPanel() {
  const { t, i18n } = useTranslation();
  const lang = i18n.language.startsWith("zh") ? "zh" : "en";

  const [providers, setProviders] = useState<Provider[]>([]);
  const [tree, setTree] = useState<IntentNode[]>([]);
  const [loading, setLoading] = useState(true);
  const [classifier, setClassifier] = useState<{
    configured: boolean; provider_name?: string; model?: string; source?: string; message?: string;
  } | null>(null);
  const [defaultProvider, setDefaultProvider] = useState<{
    found: boolean; id?: string; name?: string; message?: string;
  } | null>(null);

  const [question, setQuestion] = useState("");
  const [classifying, setClassifying] = useState(false);
  const [result, setResult] = useState<ClassifyResult | null>(null);
  const [autoSwitch, setAutoSwitch] = useState(true);

  const [editing, setEditing] = useState<IntentNode | null>(null);
  const [creating, setCreating] = useState<{ parent_code?: string; level: number } | null>(null);

  const refresh = async () => {
    setLoading(true);
    try {
      const [provRes, treeRes, classRes, defRes] = await Promise.all([
        fetch(`${API}/api/ccswitch/providers`).then(r => r.json()),
        fetch(`${API}/api/intent/tree`).then(r => r.json()),
        fetch(`${API}/api/intent/classifier`).then(r => r.json()),
        fetch(`${API}/api/intent/default-provider`).then(r => r.json()),
      ]);
      setProviders((provRes.items || []).filter((p: Provider) => p.name !== "default"));
      setTree(treeRes.roots || []);
      setClassifier(classRes);
      setDefaultProvider(defRes);
    } catch (e) {
      console.error(e);
    }
    setLoading(false);
  };

  useEffect(() => { refresh(); }, []);

  const handleClassify = async () => {
    if (!question.trim()) return;
    setClassifying(true);
    setResult(null);
    try {
      const r = await fetch(`${API}/api/intent/classify`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ question: question.trim(), auto_switch: autoSwitch }),
      });
      const data = await r.json();
      if (!r.ok) throw new Error(data.detail || "Classification failed");
      setResult(data);
      if (data.switched?.success) {
        if (data.switched.fallback) {
          message.info(
            lang === "zh"
              ? `未匹配意图 → 兜底切回 ${data.switched.provider_name}`
              : `No intent matched → fell back to ${data.switched.provider_name}`
          );
        } else {
          message.success(
            lang === "zh"
              ? `已自动切换到 ${data.switched.provider_name}`
              : `Auto-switched to ${data.switched.provider_name}`
          );
        }
      } else if (data.matched && !data.matched.provider_id) {
        message.warning(
          lang === "zh"
            ? "匹配到意图但未绑定供应商，请给叶子节点绑定模型"
            : "Intent matched but no provider bound — bind a model to the leaf node"
        );
      } else if (!data.matched) {
        message.info(
          lang === "zh"
            ? `未匹配到意图 → 兜底 ${data.default_provider?.name || "DeepSeek"}`
            : `No match → fallback to ${data.default_provider?.name || "DeepSeek"}`
        );
      }
    } catch (e: any) {
      message.error(e.message);
    }
    setClassifying(false);
  };

  const handleSwitch = async (providerId: string) => {
    try {
      const r = await fetch(`${API}/api/ccswitch/activate/${providerId}`, { method: "POST" });
      const data = await r.json();
      if (data.success) {
        message.success(lang === "zh" ? `已切换到 ${data.provider_name}` : `Switched to ${data.provider_name}`);
      } else {
        message.error(data.detail || "Failed");
      }
    } catch {
      message.error("Network error");
    }
  };

  const handleDelete = async (intentCode: string) => {
    Modal.confirm({
      title: lang === "zh" ? `删除节点「${intentCode}」及其子节点？` : `Delete "${intentCode}" and children?`,
      okText: lang === "zh" ? "删除" : "Delete",
      okType: "danger",
      cancelText: lang === "zh" ? "取消" : "Cancel",
      onOk: async () => {
        const r = await fetch(`${API}/api/intent/nodes/${intentCode}`, { method: "DELETE" });
        if (r.ok) {
          message.success(lang === "zh" ? "已删除" : "Deleted");
          refresh();
        } else {
          message.error("Delete failed");
        }
      },
    });
  };

  // Build tree data for antd Tree component
  const buildTreeData = (nodes: IntentNode[]): any[] =>
    nodes.map(n => ({
      key: n.intent_code,
      title: (
        <Space size={4} style={{ width: "100%" }}>
          <Text strong style={{ fontSize: 13 }}>{n.name}</Text>
          <Text type="secondary" style={{ fontSize: 11 }}>{n.intent_code}</Text>
          {!n.enabled && <Tag color="default" style={{ fontSize: 10 }}>OFF</Tag>}
          {n.level === 2 && (
            n.provider_id
              ? <Tag color="blue" style={{ fontSize: 10 }}>
                  → {providers.find(p => p.id === n.provider_id)?.name || "?"}
                </Tag>
              : <Tag color="warning" style={{ fontSize: 10 }}>
                  {lang === "zh" ? "未绑定模型" : "No model"}
                </Tag>
          )}
          <Button size="small" type="text" icon={<EditOutlined />}
                  style={{ marginLeft: "auto" }}
                  onClick={(e) => { e.stopPropagation(); setEditing(n); }} />
        </Space>
      ),
      children: n.children ? buildTreeData(n.children) : undefined,
    }));

  if (loading) return <div style={{ textAlign: "center", paddingTop: 120 }}><Spin size="large" /></div>;

  const allNodes = flatten(tree);

  return (
    <div style={{ padding: 20 }}>
      <Title level={4} style={{ color: "var(--text-primary)", marginBottom: 4 }}>
        <RobotOutlined style={{ marginRight: 8 }} />
        {lang === "zh" ? "AI 意图识别" : "AI Intent Recognition"}
      </Title>
      <Text type="secondary" style={{ fontSize: 12 }}>
        {lang === "zh"
          ? "输入问题 → AI 分类 → 自动切换到匹配的供应商，无匹配则兜底回 "
          : "Type a question → AI classifies → auto-switch; fallback to "}
        {defaultProvider?.found ? (
          <Tag color="blue" style={{ fontSize: 11, marginLeft: 2 }}>
            {defaultProvider.name}
          </Tag>
        ) : (
          <Text type="secondary" style={{ fontSize: 11 }}>
            {lang === "zh" ? "默认供应商" : "default"}
          </Text>
        )}
      </Text>

      {/* Classifier status banner */}
      {classifier && (
        classifier.configured ? (
          <Alert
            type="info"
            showIcon
            icon={<RobotOutlined />}
            style={{ marginTop: 12, fontSize: 12 }}
            message={
              <Space size={6}>
                <Text>
                  {lang === "zh" ? "分类引擎：" : "Classifier: "}
                </Text>
                <Tag color="processing" icon={<ApiOutlined />}>
                  {classifier.provider_name}
                </Tag>
                <Tag>{classifier.model}</Tag>
                <Text type="secondary" style={{ fontSize: 11 }}>
                  ({classifier.source === "env"
                    ? (lang === "zh" ? "环境变量" : "env var")
                    : (lang === "zh" ? "自动从 CC Switch 选用（最便宜）" : "auto-picked cheapest from CC Switch")})
                </Text>
              </Space>
            }
          />
        ) : (
          <Alert
            type="warning"
            showIcon
            style={{ marginTop: 12, fontSize: 12 }}
            message={lang === "zh" ? "AI 未配置" : "AI not configured"}
            description={classifier.message}
          />
        )
      )}

      <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
        {/* LEFT: Classifier */}
        <Col xs={24} lg={14}>
          <Card bordered={false} style={{ borderRadius: 10, border: "1px solid var(--border-light)" }}
                bodyStyle={{ padding: 20 }}>
            <Space direction="vertical" size={12} style={{ width: "100%" }}>
              <div>
                <Text strong style={{ fontSize: 13 }}>
                  <BulbOutlined style={{ marginRight: 6, color: "#f59e0b" }} />
                  {lang === "zh" ? "测试问题" : "Test Question"}
                </Text>
              </div>
              <TextArea
                value={question}
                onChange={e => setQuestion(e.target.value)}
                placeholder={lang === "zh"
                  ? "例如：设计一个分布式任务调度系统"
                  : "e.g., Design a distributed task scheduling system"}
                autoSize={{ minRows: 3, maxRows: 6 }}
                onPressEnter={(e) => { if (!e.shiftKey) { e.preventDefault(); handleClassify(); } }}
              />
              <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between" }}>
                <Space>
                  <Switch checked={autoSwitch} onChange={setAutoSwitch} size="small" />
                  <Text style={{ fontSize: 12 }}>
                    {lang === "zh" ? "分类后自动切换供应商" : "Auto-switch after classification"}
                  </Text>
                </Space>
                <Button type="primary" icon={<ThunderboltOutlined />}
                        loading={classifying} onClick={handleClassify}
                        disabled={!question.trim()}>
                  {lang === "zh" ? "AI 分析" : "Analyze"}
                </Button>
              </div>

              {result && (
                <div style={{ marginTop: 8 }}>
                  {result.matched ? (
                    <Alert
                      type={result.matched.provider_id ? "success" : "warning"}
                      showIcon
                      message={
                        <Space>
                          <CheckCircleFilled />
                          <Text strong>
                            {lang === "zh" ? "匹配：" : "Matched: "}
                            {result.matched.name}
                          </Text>
                          {result.matched.provider_name ? (
                            <Tag color="processing" icon={<ApiOutlined />}>
                              → {result.matched.provider_name}
                            </Tag>
                          ) : result.switched?.fallback ? (
                            <Tag color="blue" icon={<ApiOutlined />}>
                              {lang === "zh" ? "兜底 → " : "fallback → "}
                              {result.switched.provider_name || result.default_provider?.name}
                            </Tag>
                          ) : null}
                          <Tag>{(result.matched.score * 100).toFixed(0)}%</Tag>
                        </Space>
                      }
                      description={
                        <div style={{ fontSize: 12, marginTop: 4 }}>
                          <Text type="secondary">{result.matched.reason}</Text>
                          {!result.matched.provider_id && (
                            <Text type="secondary" style={{ display: "block", marginTop: 2 }}>
                              {lang === "zh"
                                ? "⚠️ 此意图未绑定供应商，点右侧 ✎ 编辑叶子节点绑定模型"
                                : "⚠️ No provider bound — click ✎ on the leaf to bind one"}
                            </Text>
                          )}
                        </div>
                      }
                    />
                  ) : (
                    <Alert
                      type="info" showIcon
                      message={
                        <Space>
                          <Text>
                            {lang === "zh" ? "未匹配到合适意图" : "No matching intent"}
                          </Text>
                          {result.switched?.fallback && result.switched.provider_name && (
                            <Tag color="blue" icon={<ApiOutlined />}>
                              {lang === "zh" ? "兜底 → " : "fallback → "}
                              {result.switched.provider_name}
                            </Tag>
                          )}
                        </Space>
                      }
                      description={
                        <Text type="secondary" style={{ fontSize: 11 }}>
                          {lang === "zh"
                            ? `自动切回默认供应商，或给合适的意图叶子绑定模型`
                            : `Auto-switched to default; bind a model to a leaf to fix this`}
                        </Text>
                      }
                    />
                  )}

                  {/* Candidate list */}
                  <div style={{ marginTop: 12 }}>
                    <Text type="secondary" style={{ fontSize: 11 }}>
                      {lang === "zh" ? "所有候选：" : "All candidates:"}
                    </Text>
                    {result.candidates.slice(0, 5).map((c, i) => (
                      <div key={c.intent_code} style={{
                        marginTop: 8, padding: 10,
                        background: i === 0 ? "var(--bg-active)" : "var(--bg-secondary)",
                        borderRadius: 6, border: "1px solid var(--border-light)",
                      }}>
                        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                          <Space size={6}>
                            <Text strong style={{ fontSize: 12 }}>{c.name}</Text>
                            {c.provider_name && <Tag color="blue" style={{ fontSize: 10 }}>{c.provider_name}</Tag>}
                          </Space>
                          <Text style={{ fontSize: 11, color: "var(--text-secondary)" }}>
                            {(c.score * 100).toFixed(0)}%
                          </Text>
                        </div>
                        <Progress percent={Math.round(c.score * 100)} size="small" showInfo={false}
                                  strokeColor={i === 0 ? "var(--green)" : "var(--text-muted)"} style={{ marginTop: 4 }} />
                        <Text type="secondary" style={{ fontSize: 11, display: "block", marginTop: 4 }}>
                          {c.reason}
                        </Text>
                      </div>
                    ))}
                  </div>
                </div>
              )}
            </Space>
          </Card>
        </Col>

        {/* RIGHT: Intent tree */}
        <Col xs={24} lg={10}>
          <Card
            bordered={false}
            style={{ borderRadius: 10, border: "1px solid var(--border-light)" }}
            bodyStyle={{ padding: 16 }}
            title={
              <Space>
                <BranchesOutlined />
                <Text strong>{lang === "zh" ? "意图树" : "Intent Tree"}</Text>
                <Text type="secondary" style={{ fontSize: 11 }}>
                  ({allNodes.length} {lang === "zh" ? "节点" : "nodes"})
                </Text>
              </Space>
            }
            extra={
              <Space>
                <Tooltip title={lang === "zh" ? "刷新" : "Refresh"}>
                  <Button size="small" icon={<ReloadOutlined />} onClick={refresh} />
                </Tooltip>
                <Button size="small" type="primary" icon={<PlusOutlined />}
                        onClick={() => setCreating({ level: 0 })}>
                  {lang === "zh" ? "新增" : "Add"}
                </Button>
              </Space>
            }
          >
            {tree.length === 0 ? (
              <Empty description={lang === "zh" ? "意图树为空" : "Empty tree"} />
            ) : (
              <Tree
                treeData={buildTreeData(tree)}
                defaultExpandAll
                showLine
                blockNode
                style={{ background: "transparent" }}
              />
            )}
            <Divider style={{ margin: "12px 0" }} />
            <Alert
              type="info"
              showIcon
              icon={<ApiOutlined />}
              style={{ fontSize: 12 }}
              message={lang === "zh"
                ? "点击节点旁的 ✎ 给意图绑定模型供应商"
                : "Click ✎ next to a node to bind a model provider"}
              description={lang === "zh"
                ? "绑定了供应商的叶子节点会被自动识别并切换。当前未绑定的叶子不会触发切换。"
                : "Bound leaves get auto-routed. Unbound leaves won't trigger a switch."}
            />
          </Card>
        </Col>
      </Row>

      {/* Edit / Create modal */}
      <NodeEditModal
        visible={!!editing}
        node={editing}
        providers={providers}
        allNodes={allNodes}
        onClose={() => setEditing(null)}
        onSaved={refresh}
      />
      <NodeEditModal
        visible={!!creating}
        node={null}
        providers={providers}
        allNodes={allNodes}
        parentCode={creating?.parent_code}
        initialLevel={creating?.level}
        onClose={() => setCreating(null)}
        onSaved={refresh}
      />
    </div>
  );
}

function flatten(nodes: IntentNode[]): IntentNode[] {
  const out: IntentNode[] = [];
  const walk = (ns: IntentNode[]) => ns.forEach(n => {
    out.push(n);
    if (n.children) walk(n.children);
  });
  walk(nodes);
  return out;
}

function NodeEditModal({
  visible, node, providers, allNodes, parentCode, initialLevel, onClose, onSaved,
}: {
  visible: boolean; node: IntentNode | null; providers: Provider[];
  allNodes: IntentNode[]; parentCode?: string; initialLevel?: number;
  onClose: () => void; onSaved: () => void;
}) {
  const { i18n } = useTranslation();
  const lang = i18n.language.startsWith("zh") ? "zh" : "en";
  const [form] = Form.useForm();

  useEffect(() => {
    if (visible) {
      if (node) {
        form.setFieldsValue({
          ...node, examples: (node.examples || []).join("\n"),
        });
      } else {
        form.resetFields();
        form.setFieldsValue({
          parent_code: parentCode || null,
          level: initialLevel ?? 2,
          enabled: true,
          sort_order: 0,
          examples: "",
        });
      }
    }
  }, [visible, node, form, parentCode, initialLevel]);

  const handleSubmit = async () => {
    const v = await form.validateFields();
    const payload = {
      ...v,
      examples: (v.examples || "").split("\n").map((s: string) => s.trim()).filter(Boolean),
    };
    let r;
    if (node) {
      r = await fetch(`${API}/api/intent/nodes/${node.intent_code}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
    } else {
      r = await fetch(`${API}/api/intent/nodes`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
    }
    if (r.ok) {
      message.success(lang === "zh" ? "已保存" : "Saved");
      onSaved();
      onClose();
    } else {
      const err = await r.json().catch(() => ({ detail: "Save failed" }));
      message.error(err.detail || "Save failed");
    }
  };

  return (
    <Modal
      title={node
        ? (lang === "zh" ? `编辑：${node.name}` : `Edit: ${node.name}`)
        : (lang === "zh" ? "新增节点" : "Add Node")}
      open={visible}
      onCancel={onClose}
      onOk={handleSubmit}
      okText={lang === "zh" ? "保存" : "Save"}
      cancelText={lang === "zh" ? "取消" : "Cancel"}
      width={600}
    >
      <Form form={form} layout="vertical" style={{ marginTop: 16 }}>
        <Row gutter={12}>
          <Col span={12}>
            <Form.Item name="intent_code" label={lang === "zh" ? "代码" : "Code"}
                       rules={[{ required: true }]}>
              <Input placeholder="e.g., t-arch" disabled={!!node} />
            </Form.Item>
          </Col>
          <Col span={12}>
            <Form.Item name="name" label={lang === "zh" ? "名称" : "Name"}
                       rules={[{ required: true }]}>
              <Input placeholder={lang === "zh" ? "显示名称" : "Display name"} />
            </Form.Item>
          </Col>
        </Row>
        <Row gutter={12}>
          <Col span={12}>
            <Form.Item name="level" label={lang === "zh" ? "层级" : "Level"}>
              <Select options={[
                { value: 0, label: "Domain" },
                { value: 1, label: "Category" },
                { value: 2, label: "Topic (Leaf)" },
              ]} />
            </Form.Item>
          </Col>
          <Col span={12}>
            <Form.Item name="parent_code" label={lang === "zh" ? "父节点" : "Parent"}>
              <Select allowClear options={allNodes
                .filter(n => n.intent_code !== node?.intent_code)
                .map(n => ({ value: n.intent_code, label: `[L${n.level}] ${n.name}` }))} />
            </Form.Item>
          </Col>
        </Row>
        <Form.Item name="description" label={lang === "zh" ? "描述（AI 分类用）" : "Description (used by AI)"}>
          <Input placeholder={lang === "zh" ? "简要描述" : "Brief semantic description"} />
        </Form.Item>
        <Form.Item name="examples" label={lang === "zh" ? "示例问题（每行一个）" : "Example questions (one per line)"}>
          <TextArea autoSize={{ minRows: 2, maxRows: 5 }} />
        </Form.Item>
        <Row gutter={12}>
          <Col span={12}>
            <Form.Item name="provider_id" label={lang === "zh" ? "绑定供应商" : "Bind Provider"}>
              <Select allowClear placeholder={lang === "zh" ? "选择供应商" : "Select provider"}
                      options={providers.map(p => ({ value: p.id, label: p.name }))} />
            </Form.Item>
          </Col>
          <Col span={6}>
            <Form.Item name="sort_order" label={lang === "zh" ? "排序" : "Sort"}>
              <Input type="number" />
            </Form.Item>
          </Col>
          <Col span={6}>
            <Form.Item name="enabled" label={lang === "zh" ? "启用" : "Enabled"} valuePropName="checked">
              <Switch />
            </Form.Item>
          </Col>
        </Row>
      </Form>
    </Modal>
  );
}