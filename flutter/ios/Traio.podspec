#
# Vendors the gomobile-built Go backend (Traio.xcframework) into the iOS app.
# Build the framework with `make ios-framework` from the repo root before
# `pod install` / building the app.
#
Pod::Spec.new do |s|
  s.name             = 'Traio'
  s.version          = '0.1.0'
  s.summary          = 'Traio Go backend (gomobile bind) for iOS.'
  s.description      = 'In-process HTTP backend compiled from Go via gomobile.'
  s.homepage         = 'https://github.com/nite/traio'
  s.license          = { :type => 'Proprietary' }
  s.author           = { 'Traio' => 'dev@traio.local' }
  s.source           = { :path => '.' }
  s.platform         = :ios, '13.0'
  s.vendored_frameworks = 'Frameworks/Traio.xcframework'
end
