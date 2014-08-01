# -*- mode: ruby -*-
# vi: set ft=ruby :

VAGRANTFILE_API_VERSION = "2"

Vagrant.configure(VAGRANTFILE_API_VERSION) do |config|

  config.vm.box = "km-demo"
  config.vm.box_url = "http://downloads.mesosphere.io/demo/km-demo.box"

  config.vm.synced_folder ".", "/vagrant", :disabled => true
  config.vm.synced_folder "./", "/home/vagrant/hostfiles"

  config.ssh.forward_agent = true

  config.vm.define "mesos-1" do |m1|
    m1.vm.network :private_network, ip: "10.141.141.10"
    m1.vm.hostname = "mesos-1"
  end

  config.vm.define "mesos-2" do |m2|
    m2.vm.network :private_network, ip: "10.141.141.11"
    m2.vm.hostname = "mesos-2"
  end

  config.vm.define "mesos-3" do |m3|
    m3.vm.network :private_network, ip: "10.141.141.12"
    m3.vm.hostname = "mesos-3"
  end

  config.vm.provider :virtualbox do |vb|
    vb.customize ["modifyvm", :id, "--ioapic", "on"]
    vb.customize ["modifyvm", :id, "--cpus", "1"]
    vb.customize ["modifyvm", :id, "--memory", "512"]
  end

end
