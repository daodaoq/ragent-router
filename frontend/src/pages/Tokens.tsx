// API Key 管理页面
import { useEffect, useState } from "react";
import { Card, Table, Tag, Button, Space, Modal, Form, Input, InputNumber, Switch, message, Popconfirm, Typography } from "antd";
import { PlusOutlined, ReloadOutlined, CopyOutlined } from "@ant-design/icons";
import { request } from "../api/client";

const { Text } = Typography;

interface Token {
  id: number;
  name: string;
  key: string; // 掩码后的
  status: number;
  created_time: number;
  accessed_time: number;
  expired_time: number;
  remain_quota: number;
  unlimited_quota: boolean;
  used_quota: number;
  group: string;
}

const statusMap: Record<number, { color: string; text: string }> = {
  1: { color: "green", text: "启用" },
  2: { color: "red", text: "禁用" },
  3: { color: "orange", text: "已耗尽" },
};

export default function Tokens() {
  const [tokens, setTokens] = useState<Token[]>([]);
  const [loading, setLoading] = useState(false);
  const [modalOpen, setModalOpen] = useState(false);
  const [createdKey, setCreatedKey] = useState<string | null>(null);
  const [form] = Form.useForm();

  const fetchTokens = async () => {
    setLoading(true);
    try {
      const res = await request<{ success: boolean; data: Token[] }>("/api/tokens");
      setTokens(res?.data ?? []);
    } catch (err: any) {
      message.error(err.message);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchTokens();
  }, []);

  const handleCreate = async () => {
    try {
      const values = await form.validateFields();
      const res = await request<{ success: boolean; data?: { key: string }; message: string }>("/api/tokens", {
        method: "POST",
        body: JSON.stringify(values),
      });
      if (res?.data?.key) {
        setCreatedKey(res.data.key);
      } else {
        message.success("创建成功");
        setModalOpen(false);
        fetchTokens();
      }
    } catch (err: any) {
      message.error(err.message);
    }
  };

  const handleDelete = async (id: number) => {
    try {
      await request(`/api/tokens/${id}`, { method: "DELETE" });
      message.success("删除成功");
      fetchTokens();
    } catch (err: any) {
      message.error(err.message);
    }
  };

  const handleCopyKey = () => {
    if (createdKey) {
      navigator.clipboard.writeText(createdKey);
      message.success("已复制到剪贴板");
    }
  };

  const columns = [
    { title: "ID", dataIndex: "id", key: "id", width: 60 },
    { title: "名称", dataIndex: "name", key: "name", width: 120 },
    {
      title: "Key",
      dataIndex: "key",
      key: "key",
      width: 200,
      render: (key: string) => (
        <Text code style={{ fontSize: 12 }}>
          {key}
        </Text>
      ),
    },
    {
      title: "状态",
      dataIndex: "status",
      key: "status",
      width: 80,
      render: (s: number) => {
        const info = statusMap[s] || { color: "default", text: "未知" };
        return <Tag color={info.color}>{info.text}</Tag>;
      },
    },
    {
      title: "配额",
      key: "quota",
      width: 120,
      render: (_: any, record: Token) =>
        record.unlimited_quota ? (
          <Tag color="blue">无限</Tag>
        ) : (
          <Text style={{ fontSize: 12 }}>{record.remain_quota.toLocaleString()}</Text>
        ),
    },
    {
      title: "已用",
      dataIndex: "used_quota",
      key: "used_quota",
      width: 100,
      render: (v: number) => <Text style={{ fontSize: 12 }}>{v.toLocaleString()}</Text>,
    },
    {
      title: "创建时间",
      dataIndex: "created_time",
      key: "created_time",
      width: 150,
      render: (v: number) => (v ? new Date(v * 1000).toLocaleString() : "-"),
    },
    {
      title: "操作",
      key: "action",
      width: 100,
      render: (_: any, record: Token) => (
        <Popconfirm title="确认删除？" onConfirm={() => handleDelete(record.id)}>
          <Button size="small" danger>
            删除
          </Button>
        </Popconfirm>
      ),
    },
  ];

  return (
    <div style={{ padding: 20 }}>
      <Card
        title="API Key 管理"
        bordered={false}
        extra={
          <Space>
            <Button icon={<ReloadOutlined />} onClick={fetchTokens}>
              刷新
            </Button>
            <Button
              type="primary"
              icon={<PlusOutlined />}
              onClick={() => {
                form.resetFields();
                setCreatedKey(null);
                setModalOpen(true);
              }}
            >
              创建 Key
            </Button>
          </Space>
        }
      >
        <Table dataSource={tokens} columns={columns} rowKey="id" loading={loading} size="small" scroll={{ x: 900 }} />
      </Card>

      <Modal
        title="创建 API Key"
        open={modalOpen}
        onOk={createdKey ? () => { setModalOpen(false); fetchTokens(); } : handleCreate}
        onCancel={() => setModalOpen(false)}
        okText={createdKey ? "完成" : "创建"}
      >
        {createdKey ? (
          <div style={{ textAlign: "center", padding: 20 }}>
            <p style={{ marginBottom: 16, color: "var(--text-secondary)" }}>请妥善保管，仅显示一次：</p>
            <div
              style={{
                background: "var(--bg-elevated)",
                padding: 16,
                borderRadius: 8,
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                gap: 8,
              }}
            >
              <Text code style={{ fontSize: 14, wordBreak: "break-all" }}>
                {createdKey}
              </Text>
              <Button icon={<CopyOutlined />} size="small" onClick={handleCopyKey} />
            </div>
          </div>
        ) : (
          <Form form={form} layout="vertical">
            <Form.Item name="name" label="名称" rules={[{ required: true }]}>
              <Input placeholder="如：我的应用" />
            </Form.Item>
            <Form.Item name="quota" label="配额" initialValue={1000000}>
              <InputNumber min={0} style={{ width: "100%" }} />
            </Form.Item>
            <Form.Item name="unlimited_quota" label="无限配额" valuePropName="checked">
              <Switch />
            </Form.Item>
          </Form>
        )}
      </Modal>
    </div>
  );
}
