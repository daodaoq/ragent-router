// 登录/注册页面
import React, { useState } from "react";
import { Card, Form, Input, Button, Typography, Space, message, Tabs } from "antd";
import { UserOutlined, LockOutlined } from "@ant-design/icons";
import { login, register, setToken } from "../api/client";

const { Title, Text } = Typography;

interface LoginProps {
  onLogin: () => void;
}

const Login: React.FC<LoginProps> = ({ onLogin }) => {
  const [loading, setLoading] = useState(false);
  const [activeTab, setActiveTab] = useState("login");

  const handleLogin = async (values: { username: string; password: string }) => {
    setLoading(true);
    try {
      const res = await login(values.username, values.password);
      if (res.success && res.data?.token) {
        setToken(res.data.token);
        message.success("登录成功");
        onLogin();
      } else {
        message.error(res.message || "登录失败");
      }
    } catch (err: any) {
      message.error(err.message || "登录失败");
    } finally {
      setLoading(false);
    }
  };

  const handleRegister = async (values: { username: string; password: string }) => {
    setLoading(true);
    try {
      const res = await register(values.username, values.password);
      if (res.success) {
        message.success("注册成功，请登录");
        setActiveTab("login");
      } else {
        message.error(res.message || "注册失败");
      }
    } catch (err: any) {
      message.error(err.message || "注册失败");
    } finally {
      setLoading(false);
    }
  };

  return (
    <div
      style={{
        display: "flex",
        justifyContent: "center",
        alignItems: "center",
        minHeight: "100vh",
        background: "linear-gradient(135deg, #667eea 0%, #764ba2 100%)",
      }}
    >
      <Card style={{ width: 400, borderRadius: 12 }} bordered={false}>
        <Space direction="vertical" style={{ width: "100%", textAlign: "center", marginBottom: 24 }}>
          <Title level={3} style={{ margin: 0 }}>
            RAgent Router
          </Title>
          <Text type="secondary">AI API 智能网关</Text>
        </Space>

        <Tabs
          activeKey={activeTab}
          onChange={setActiveTab}
          centered
          items={[
            {
              key: "login",
              label: "登录",
              children: (
                <Form onFinish={handleLogin} size="large">
                  <Form.Item name="username" rules={[{ required: true, message: "请输入用户名" }]}>
                    <Input prefix={<UserOutlined />} placeholder="用户名" />
                  </Form.Item>
                  <Form.Item name="password" rules={[{ required: true, message: "请输入密码" }]}>
                    <Input.Password prefix={<LockOutlined />} placeholder="密码" />
                  </Form.Item>
                  <Form.Item>
                    <Button type="primary" htmlType="submit" loading={loading} block>
                      登录
                    </Button>
                  </Form.Item>
                </Form>
              ),
            },
            {
              key: "register",
              label: "注册",
              children: (
                <Form onFinish={handleRegister} size="large">
                  <Form.Item
                    name="username"
                    rules={[
                      { required: true, message: "请输入用户名" },
                      { min: 3, message: "用户名至少 3 个字符" },
                    ]}
                  >
                    <Input prefix={<UserOutlined />} placeholder="用户名" />
                  </Form.Item>
                  <Form.Item
                    name="password"
                    rules={[
                      { required: true, message: "请输入密码" },
                      { min: 6, message: "密码至少 6 个字符" },
                    ]}
                  >
                    <Input.Password prefix={<LockOutlined />} placeholder="密码" />
                  </Form.Item>
                  <Form.Item>
                    <Button type="primary" htmlType="submit" loading={loading} block>
                      注册
                    </Button>
                  </Form.Item>
                </Form>
              ),
            },
          ]}
        />

        <div style={{ textAlign: "center", marginTop: 16 }}>
          <Text type="secondary" style={{ fontSize: 12 }}>
            Mock 模式默认用户: root / 123456
          </Text>
        </div>
      </Card>
    </div>
  );
};

export default Login;
