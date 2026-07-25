// 渠道管理页面
import { useEffect, useState } from "react";
import { Card, Table, Tag, Button, Space, Modal, Form, Input, InputNumber, Select, message, Popconfirm } from "antd";
import { PlusOutlined, ReloadOutlined } from "@ant-design/icons";
import { request } from "../api/client";

interface Channel {
  id: number;
  name: string;
  type: number;
  base_url: string;
  models: string;
  weight: number;
  priority: number;
  status: number;
  group: string;
  tag: string;
  remark: string;
}

const statusMap: Record<number, { color: string; text: string }> = {
  1: { color: "green", text: "启用" },
  2: { color: "red", text: "禁用" },
  3: { color: "orange", text: "自动禁用" },
};

export default function Channels() {
  const [channels, setChannels] = useState<Channel[]>([]);
  const [loading, setLoading] = useState(false);
  const [modalOpen, setModalOpen] = useState(false);
  const [editing, setEditing] = useState<Channel | null>(null);
  const [form] = Form.useForm();

  const fetchChannels = async () => {
    setLoading(true);
    try {
      const res = await request<{ success: boolean; data: Channel[] }>("/api/channels");
      setChannels(res?.data ?? []);
    } catch (err: any) {
      message.error(err.message);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchChannels();
  }, []);

  const handleAdd = () => {
    setEditing(null);
    form.resetFields();
    setModalOpen(true);
  };

  const handleEdit = (record: Channel) => {
    setEditing(record);
    form.setFieldsValue(record);
    setModalOpen(true);
  };

  const handleDelete = async (id: number) => {
    try {
      await request(`/api/channels/${id}`, { method: "DELETE" });
      message.success("删除成功");
      fetchChannels();
    } catch (err: any) {
      message.error(err.message);
    }
  };

  const handleSubmit = async () => {
    try {
      const values = await form.validateFields();
      if (editing) {
        await request(`/api/channels/${editing.id}`, {
          method: "PUT",
          body: JSON.stringify(values),
        });
        message.success("更新成功");
      } else {
        await request("/api/channels", {
          method: "POST",
          body: JSON.stringify(values),
        });
        message.success("创建成功");
      }
      setModalOpen(false);
      fetchChannels();
    } catch (err: any) {
      message.error(err.message);
    }
  };

  const columns = [
    { title: "ID", dataIndex: "id", key: "id", width: 60 },
    { title: "名称", dataIndex: "name", key: "name", width: 120 },
    {
      title: "状态",
      dataIndex: "status",
      key: "status",
      width: 100,
      render: (s: number) => {
        const info = statusMap[s] || { color: "default", text: "未知" };
        return <Tag color={info.color}>{info.text}</Tag>;
      },
    },
    { title: "Base URL", dataIndex: "base_url", key: "base_url", ellipsis: true },
    { title: "模型", dataIndex: "models", key: "models", ellipsis: true },
    { title: "权重", dataIndex: "weight", key: "weight", width: 70 },
    { title: "优先级", dataIndex: "priority", key: "priority", width: 80 },
    { title: "分组", dataIndex: "group", key: "group", width: 80 },
    {
      title: "操作",
      key: "action",
      width: 150,
      render: (_: any, record: Channel) => (
        <Space size="small">
          <Button size="small" onClick={() => handleEdit(record)}>
            编辑
          </Button>
          <Popconfirm title="确认删除？" onConfirm={() => handleDelete(record.id)}>
            <Button size="small" danger>
              删除
            </Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div style={{ padding: 20 }}>
      <Card
        title="渠道管理"
        bordered={false}
        extra={
          <Space>
            <Button icon={<ReloadOutlined />} onClick={fetchChannels}>
              刷新
            </Button>
            <Button type="primary" icon={<PlusOutlined />} onClick={handleAdd}>
              添加渠道
            </Button>
          </Space>
        }
      >
        <Table
          dataSource={channels}
          columns={columns}
          rowKey="id"
          loading={loading}
          size="small"
          scroll={{ x: 1000 }}
        />
      </Card>

      <Modal
        title={editing ? "编辑渠道" : "添加渠道"}
        open={modalOpen}
        onOk={handleSubmit}
        onCancel={() => setModalOpen(false)}
        width={600}
      >
        <Form form={form} layout="vertical">
          <Form.Item name="name" label="名称" rules={[{ required: true }]}>
            <Input placeholder="如：DeepSeek" />
          </Form.Item>
          <Form.Item name="key" label="API Key" rules={[{ required: !editing }]}>
            <Input.Password placeholder="sk-xxx" />
          </Form.Item>
          <Form.Item name="base_url" label="Base URL">
            <Input placeholder="https://api.deepseek.com" />
          </Form.Item>
          <Form.Item name="models" label="模型列表" help="逗号分隔">
            <Input placeholder="deepseek-chat,deepseek-coder" />
          </Form.Item>
          <Form.Item name="type" label="渠道类型" initialValue={0}>
            <Select>
              <Select.Option value={0}>OpenAI 兼容</Select.Option>
              <Select.Option value={14}>Anthropic</Select.Option>
              <Select.Option value={24}>Gemini</Select.Option>
            </Select>
          </Form.Item>
          <Space>
            <Form.Item name="weight" label="权重" initialValue={1}>
              <InputNumber min={1} max={100} />
            </Form.Item>
            <Form.Item name="priority" label="优先级" initialValue={0}>
              <InputNumber min={0} max={999} />
            </Form.Item>
            <Form.Item name="group" label="分组" initialValue="default">
              <Input />
            </Form.Item>
          </Space>
          <Form.Item name="remark" label="备注">
            <Input.TextArea rows={2} />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
