import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom';
import Layout from './components/Layout';
import Login from './pages/Login';
import Fleet from './pages/Fleet';
import Devices from './pages/Devices';
import DeviceDetail from './pages/DeviceDetail';
import Bundles from './pages/Bundles';
import Rollouts from './pages/Rollouts';

export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route element={<Layout />}>
          <Route path="/" element={<Fleet />} />
          <Route path="/devices" element={<Devices />} />
          <Route path="/devices/:id" element={<DeviceDetail />} />
          <Route path="/bundles" element={<Bundles />} />
          <Route path="/rollouts" element={<Rollouts />} />
        </Route>
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </BrowserRouter>
  );
}
